package skills

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/egress"
)

const (
	// maxRepoDownloadBytes bounds the compressed archive of a repository, which holds far more than the one skill a link points at
	maxRepoDownloadBytes = 256 << 20
	importTimeout        = 2 * time.Minute
	// maxRefCandidates bounds the archives tried for a link whose branch name may contain slashes
	maxRefCandidates = 5
	// defaultGitHubArchives serves the tarball of any public repository at any ref
	defaultGitHubArchives = "https://codeload.github.com"
	// maxScanBytes bounds what one read of a repository keeps in memory across all the skills it picks
	maxScanBytes = 64 << 20
	// maxChoices bounds the skills a preview lists
	maxChoices = 100
)

// githubNameRe matches the owner and repository names GitHub allows, which keeps them from changing the archive URL's path
var githubNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// githubLink is a link to a folder or file in a GitHub repository
type githubLink struct {
	owner string
	repo  string
	// candidates are the ref and folder pairs the link can mean, tried in order, since a branch name may contain slashes
	candidates []refDir
}

type refDir struct {
	ref string
	dir string
}

// parseGitHubLink reads a link to a repository, folder or file on github.com, and reports false for anything else, such as a release download
func parseGitHubLink(u *url.URL) (githubLink, bool) {
	if u.Host != "github.com" && u.Host != "www.github.com" {
		return githubLink{}, false
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) < 2 {
		return githubLink{}, false
	}
	link := githubLink{owner: segments[0], repo: strings.TrimSuffix(segments[1], ".git")}
	if !githubNameRe.MatchString(link.owner) || !githubNameRe.MatchString(link.repo) {
		return githubLink{}, false
	}

	// A repository's own page stands for its default branch
	if len(segments) == 2 {
		link.candidates = []refDir{{ref: "HEAD"}}
		return link, true
	}

	// A file stands for the folder it is in, so a link to a SKILL.md works too
	rest := segments[3:]
	switch {
	case segments[2] == "tree" && len(rest) >= 1:
	case segments[2] == "blob" && len(rest) >= 2:
		rest = rest[:len(rest)-1]
	default:
		return githubLink{}, false
	}
	for i := 1; i <= len(rest) && i <= maxRefCandidates; i++ {
		link.candidates = append(link.candidates, refDir{ref: strings.Join(rest[:i], "/"), dir: strings.Join(rest[i:], "/")})
	}
	return link, true
}

// resolveLink checks a link before anything is downloaded and reads it as a GitHub link when it is one
func (m *Module) resolveLink(ctx context.Context, rawURL string) (*url.URL, githubLink, bool, error) {
	if err := m.deps.Egress.CheckURL(ctx, "url", rawURL); err != nil {
		return nil, githubLink{}, false, err
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, githubLink{}, false, onField("url", invalid("invalid", "must be an http(s) URL"))
	}
	link, ok := parseGitHubLink(u)
	return u, link, ok, nil
}

// importFrom downloads the skill a link points at, a folder on GitHub or a zip or .skill file anywhere else
func (m *Module) importFrom(ctx context.Context, rawURL string) (*Archive, error) {
	_, link, isGitHub, err := m.resolveLink(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	client := m.deps.Egress.HTTPClient(importTimeout)
	if !isGitHub {
		a, err := fetchZip(ctx, client, rawURL)
		return a, onField("url", err)
	}
	var a *Archive
	err = m.downloadRepo(ctx, client, link, func(body io.Reader, _ refDir, dir string) error {
		a, err = extractFolder(body, dir)
		return err
	})
	return a, onField("url", err)
}

// pickedSkill is one of the skills an import picked from a folder, with the link to its own folder
type pickedSkill struct {
	archive *Archive
	link    string
}

// importPicked downloads a GitHub folder once and reads the skills in the given subfolders of it
func (m *Module) importPicked(ctx context.Context, rawURL string, dirs []string) ([]pickedSkill, error) {
	_, link, isGitHub, err := m.resolveLink(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	if !isGitHub {
		return nil, onField("url", invalid("not_a_folder", "must be a GitHub folder to pick skills from"))
	}
	for _, d := range dirs {
		if !fs.ValidPath(d) || d == "." {
			return nil, apperror.InvalidField("paths", "invalid", fmt.Sprintf("contains the invalid folder %q", d))
		}
	}

	var picked []pickedSkill
	err = m.downloadRepo(ctx, m.deps.Egress.HTTPClient(importTimeout), link, func(body io.Reader, c refDir, dir string) error {
		scan, err := scanFolder(body, dir, func(rel string) (string, string, bool) {
			for _, d := range dirs {
				if inner, ok := strings.CutPrefix(rel, d+"/"); ok {
					return d, inner, true
				}
			}
			return "", "", false
		})
		if err != nil {
			return err
		}

		// Every picked folder must hold a skill, so a stale choice fails rather than adding part of what was picked
		for _, d := range dirs {
			g := scan.groups[d]
			if g == nil || !slices.Contains(scan.skillDirs, d) {
				return invalid("not_found", fmt.Sprintf("has no skill in %s", d))
			}
			if g.problem != nil {
				return prefixed(d, g.problem)
			}
			a, err := fromEntries(g.files)
			if err != nil {
				return prefixed(d, err)
			}
			picked = append(picked, pickedSkill{archive: a, link: link.folderURL(c.ref, path.Join(dir, d))})
		}
		return nil
	})
	return picked, onField("url", err)
}

// SkillChoice is one skill in a folder of several, which an import can pick
type SkillChoice struct {
	Path        string `json:"path" doc:"The skill's folder, relative to the folder the link points at"`
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url" doc:"A link to the skill's own folder"`
	Exists      bool   `json:"exists" doc:"Whether the workspace already has a skill with this name"`
	Problem     string `json:"problem,omitempty" doc:"Why the skill can't be added, such as a SKILL.md without a description"`
}

// previewLink lists the skills in the GitHub folder a link points at, reading only their SKILL.md files
func (m *Module) previewLink(ctx context.Context, rawURL string) ([]SkillChoice, error) {
	_, link, isGitHub, err := m.resolveLink(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	if !isGitHub {
		return nil, onField("url", invalid("not_a_folder", "must be a GitHub folder to list its skills"))
	}

	var choices []SkillChoice
	err = m.downloadRepo(ctx, m.deps.Egress.HTTPClient(importTimeout), link, func(body io.Reader, c refDir, dir string) error {
		scan, err := scanFolder(body, dir, func(rel string) (string, string, bool) {
			if path.Base(rel) != skillFile {
				return "", "", false
			}
			return path.Dir(rel), skillFile, true
		})
		if err != nil {
			return err
		}
		slices.Sort(scan.skillDirs)
		for _, d := range scan.skillDirs {
			if len(choices) == maxChoices {
				break
			}
			choice := SkillChoice{Path: d, URL: link.folderURL(c.ref, path.Join(dir, d))}
			// A skill that can't be added is still listed, under its folder's name, with the reason
			g := scan.groups[d]
			switch {
			case g == nil || len(g.files) == 0:
				choice.Name, choice.Problem = path.Base(d), "It could not be read"
			case g.problem != nil:
				choice.Name, choice.Problem = path.Base(d), "It "+g.problem.Error()
			default:
				choice.Name, choice.Description, err = parseFrontmatter(g.files[0].content)
				if err != nil {
					choice.Name, choice.Problem = path.Base(d), "It "+err.Error()
				}
			}
			choices = append(choices, choice)
		}
		return nil
	})
	return choices, onField("url", err)
}

// prefixed names the skill folder a problem came from, since a pick reads several at once
func prefixed(dir string, err error) error {
	var ae *archiveError
	if errors.As(err, &ae) {
		return invalid(ae.code, fmt.Sprintf("%s (in %s)", ae.message, dir))
	}
	return err
}

// folderURL is the GitHub link to a folder of the repository at a ref
func (l githubLink) folderURL(ref, dir string) string {
	u := "https://github.com/" + l.owner + "/" + l.repo + "/tree/" + escapePath(ref)
	if dir != "" && dir != "." {
		u += "/" + escapePath(dir)
	}
	return u
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

// get starts a download, reporting a failed connection as a problem with the link
func get(ctx context.Context, client *http.Client, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, invalid("invalid", "must be an http(s) URL")
	}
	resp, err := client.Do(req)
	if errors.Is(err, egress.ErrBlocked) {
		return nil, invalid("forbidden", "points to a private or local network address")
	} else if err != nil {
		return nil, invalid("unreachable", "could not be downloaded")
	}
	return resp, nil
}

// fetchZip downloads a zip or .skill file
func fetchZip(ctx context.Context, client *http.Client, rawURL string) (*Archive, error) {
	resp, err := get(ctx, client, rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, invalid("unreachable", fmt.Sprintf("answered with HTTP %d", resp.StatusCode))
	}
	body, err := io.ReadAll(egress.LimitBody(resp.Body, maxArchiveBytes))
	if errors.Is(err, egress.ErrResponseTooLarge) {
		return nil, invalid("too_large", fmt.Sprintf("must point at a zip of at most %d MiB", maxArchiveBytes>>20))
	} else if err != nil {
		return nil, invalid("unreachable", "could not be downloaded")
	}
	return parseZip(body)
}

// downloadRepo downloads the repository's archive and hands it to read with the ref and folder the link turned out to mean
// The archive of a ref that doesn't exist answers 404, so each reading of the link is tried until one exists
func (m *Module) downloadRepo(ctx context.Context, client *http.Client, link githubLink, read func(body io.Reader, c refDir, dir string) error) error {
	for _, c := range link.candidates {
		resp, err := get(ctx, client, fmt.Sprintf("%s/%s/%s/tar.gz/%s", m.archives(ctx), link.owner, link.repo, escapePath(c.ref)))
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusNotFound {
			_ = resp.Body.Close()
			continue
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return invalid("unreachable", fmt.Sprintf("could not be downloaded, GitHub answered with HTTP %d", resp.StatusCode))
		}
		err = read(resp.Body, c, c.dir)
		_ = resp.Body.Close()
		return err
	}
	return invalid("not_found", "points at a repository or branch that doesn't exist or isn't public")
}

// fileGroup is the files one read of a repository kept for one skill folder, with paths relative to that folder
type fileGroup struct {
	files   []rawFile
	read    int64
	problem error
}

// folderScan is what one read of a repository found in the folder a link points at
type folderScan struct {
	// skillDirs are the folders with a SKILL.md, relative to the linked folder, where "." is the folder itself
	skillDirs []string
	groups    map[string]*fileGroup
}

// scanFolder reads a repository's tarball once and keeps the files under dir that pick assigns to a group, each group within the limits of one skill
// A problem with a group's files is kept on the group, so the caller decides whether it matters
func scanFolder(body io.Reader, dir string, pick func(rel string) (group, inner string, ok bool)) (*folderScan, error) {
	gz, err := gzip.NewReader(egress.LimitBody(io.NopCloser(body), maxRepoDownloadBytes))
	if err != nil {
		return nil, invalid("invalid_archive", "could not be read")
	}
	tr := tar.NewReader(gz)
	prefix := ""
	if dir != "" {
		prefix = dir + "/"
	}
	tooLarge := invalid("too_large", fmt.Sprintf("points at a repository whose archive is larger than %d MiB", maxRepoDownloadBytes>>20))

	scan := &folderScan{groups: map[string]*fileGroup{}}
	var total int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		} else if errors.Is(err, egress.ErrResponseTooLarge) {
			return nil, tooLarge
		} else if err != nil {
			return nil, invalid("invalid_archive", "could not be read")
		}

		// GitHub puts everything inside one folder named after the repository and ref
		_, name, ok := strings.Cut(hdr.Name, "/")
		if !ok {
			continue
		}
		rel, ok := strings.CutPrefix(name, prefix)
		if !ok || rel == "" || hdr.Typeflag == tar.TypeDir || isJunk(rel) {
			continue
		}
		if path.Base(rel) == skillFile {
			scan.skillDirs = append(scan.skillDirs, path.Dir(rel))
		}
		key, inner, ok := pick(rel)
		if !ok {
			continue
		}
		g := scan.groups[key]
		if g == nil {
			g = &fileGroup{}
			scan.groups[key] = g
		}
		if g.problem != nil {
			continue
		}

		// Only plain files become part of a skill, and anything else in its folder is refused like in a zip
		mode := fs.FileMode(hdr.Mode) & fs.ModePerm // #nosec G115 -- only the permission bits are kept
		switch hdr.Typeflag {
		case tar.TypeReg:
		case tar.TypeSymlink, tar.TypeLink:
			mode = fs.ModeSymlink
		default:
			continue
		}
		if g.problem = checkEntry(inner, mode); g.problem != nil {
			continue
		}
		if len(g.files) == maxFiles {
			g.problem = invalid("too_many_files", fmt.Sprintf("must contain at most %d files", maxFiles))
			continue
		}
		content, err := io.ReadAll(io.LimitReader(tr, maxUnpackedBytes-g.read+1))
		if errors.Is(err, egress.ErrResponseTooLarge) {
			return nil, tooLarge
		} else if err != nil {
			return nil, invalid("invalid_archive", "could not be read")
		}
		g.read += int64(len(content))
		if g.read > maxUnpackedBytes {
			g.problem = invalid("too_large", fmt.Sprintf("must unpack to at most %d MiB", maxUnpackedBytes>>20))
			g.files = nil
			continue
		}

		// Several groups together must stay within what one read may hold in memory
		total += int64(len(content))
		if total > maxScanBytes {
			return nil, invalid("too_large", fmt.Sprintf("holds more than %d MiB in the picked skills, so pick fewer at once", maxScanBytes>>20))
		}
		g.files = append(g.files, rawFile{path: inner, mode: mode, content: content})
	}
	return scan, nil
}

// extractFolder reads the skill in one folder of a repository's tarball
// The whole folder is read even after a problem, so a link to a folder of several skills says so rather than that the folder is too large
func extractFolder(body io.Reader, dir string) (*Archive, error) {
	scan, err := scanFolder(body, dir, func(rel string) (string, string, bool) { return "", rel, true })
	if err != nil {
		return nil, err
	}
	skillDirs := scan.skillDirs

	// A folder of several skills needs a link to one of them, or a pick of some, which helps more than any other problem with it
	if !slices.Contains(skillDirs, ".") && len(skillDirs) > 1 {
		slices.Sort(skillDirs)
		examples := skillDirs[:min(3, len(skillDirs))]
		return nil, invalid("several_skills", fmt.Sprintf("points at a folder with %d skills, such as %s, so link to the folder of one of them", len(skillDirs), strings.Join(examples, ", ")))
	}
	g := scan.groups[""]
	if g != nil && g.problem != nil {
		return nil, g.problem
	}
	if g == nil || len(g.files) == 0 {
		return nil, invalid("not_found", "points at a folder that is empty or doesn't exist")
	}
	files := g.files

	// A single skill further down, such as in a repository with a README next to the skill's folder, is taken on its own
	if len(skillDirs) == 1 && skillDirs[0] != "." {
		inner := skillDirs[0] + "/"
		kept := files[:0]
		for _, f := range files {
			if rel, ok := strings.CutPrefix(f.path, inner); ok {
				f.path = rel
				kept = append(kept, f)
			}
		}
		files = kept
	}
	return fromEntries(files)
}

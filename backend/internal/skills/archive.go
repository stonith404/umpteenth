package skills

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
)

const (
	// maxArchiveBytes bounds an uploaded zip, the same cap Anthropic puts on skill uploads
	maxArchiveBytes = 8 << 20
	// maxUnpackedBytes bounds what one skill unpacks to, counted on the bytes actually read so a lying header can't get past it
	maxUnpackedBytes = 32 << 20
	maxFiles         = 500
	maxPathBytes     = 512
	// maxFrontmatterBytes bounds the YAML that is parsed, so a SKILL.md can't make the parser chew on megabytes
	maxFrontmatterBytes = 16 << 10
	maxNameLength       = 64
	maxDescriptionChars = 1024

	skillFile = "SKILL.md"
)

// normalizedTime is the modification time of every entry in a normalized zip, so the same skill always zips to the same bytes
var normalizedTime = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

var (
	nameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	// tagRe finds XML-like tags, which have no place in a description that goes into the system prompt
	tagRe = regexp.MustCompile(`<[A-Za-z/!?]`)
)

// File is one file of a skill, with a path relative to the skill folder
type File struct {
	Path    string
	Mode    fs.FileMode
	Content []byte
}

// Executable reports whether the file keeps its exec bits in the sandbox
func (f File) Executable() bool { return f.Mode&0o111 != 0 }

// Archive is a parsed and validated skill
type Archive struct {
	Name        string
	Description string
	// Files are sorted by path
	Files []File
	// Size is the unpacked size of all files
	Size int64
}

// archiveError is a problem with a skill's content, reported on the request field the skill came from
type archiveError struct {
	code    string
	message string
}

func (e *archiveError) Error() string { return e.message }

func invalid(code, message string) error {
	return &archiveError{code: code, message: message}
}

// onField turns a problem with a skill's content into a validation error on the given request field
func onField(field string, err error) error {
	var ae *archiveError
	if errors.As(err, &ae) {
		return apperror.InvalidField(field, ae.code, ae.message)
	}
	return err
}

// rawFile is a file read from an upload, with its path inside the upload
type rawFile struct {
	path    string
	mode    fs.FileMode
	content []byte
}

// ParseArchive reads a skill zip, with its files at the root or inside a single top-level folder, and validates it
func ParseArchive(body []byte) (*Archive, error) {
	a, err := parseZip(body)
	return a, onField("file", err)
}

func parseZip(body []byte) (*Archive, error) {
	if len(body) > maxArchiveBytes {
		return nil, invalid("too_large", fmt.Sprintf("must be at most %d MiB", maxArchiveBytes>>20))
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if errors.Is(err, zip.ErrInsecurePath) {
		return nil, invalid("unsafe_path", "contains a path outside the skill folder")
	} else if err != nil {
		return nil, invalid("invalid_zip", "is not a valid zip file")
	}

	// Junk that archivers add is dropped before anything else, since macOS puts __MACOSX next to the skill folder
	files := make([]*zip.File, 0, len(zr.File))
	for _, f := range zr.File {
		name := strings.TrimPrefix(strings.ReplaceAll(f.Name, `\`, "/"), "./")
		if strings.HasSuffix(name, "/") || f.FileInfo().IsDir() || isJunk(name) {
			continue
		}
		files = append(files, f)
		if len(files) > maxFiles {
			return nil, invalid("too_many_files", fmt.Sprintf("must contain at most %d files", maxFiles))
		}
	}

	// Every entry must be a plain file with a path that stays inside the skill folder
	var declared uint64
	for _, f := range files {
		name := strings.TrimPrefix(strings.ReplaceAll(f.Name, `\`, "/"), "./")
		if f.Flags&0x1 != 0 {
			return nil, invalid("encrypted", "must not be encrypted")
		}
		if err := checkEntry(name, f.Mode()); err != nil {
			return nil, err
		}
		declared += f.UncompressedSize64
	}
	if declared > maxUnpackedBytes {
		return nil, invalid("too_large", fmt.Sprintf("must unpack to at most %d MiB", maxUnpackedBytes>>20))
	}

	// Read every file, counting the bytes actually read since the declared sizes come from the uploader
	entries := make([]rawFile, 0, len(files))
	var read int64
	for _, f := range files {
		content, err := readEntry(f, maxUnpackedBytes-read)
		if err != nil {
			return nil, err
		}
		read += int64(len(content))
		name := strings.TrimPrefix(strings.ReplaceAll(f.Name, `\`, "/"), "./")
		entries = append(entries, rawFile{path: name, mode: f.Mode(), content: content})
	}
	return fromEntries(entries)
}

// checkEntry refuses an entry that isn't a plain file inside the skill folder
func checkEntry(name string, mode fs.FileMode) error {
	if mode&fs.ModeType != 0 {
		return invalid("unsafe_path", fmt.Sprintf("contains %s, which is a symlink or special file", name))
	}
	if !fs.ValidPath(name) || len(name) > maxPathBytes {
		return invalid("unsafe_path", fmt.Sprintf("contains the unsafe path %q", name))
	}
	return nil
}

// fromEntries builds a skill from the files of an upload, whose skill folder is its root or its only top-level folder
func fromEntries(entries []rawFile) (*Archive, error) {
	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = e.path
	}
	root, err := skillRoot(paths)
	if err != nil {
		return nil, err
	}

	// Paths become relative to the skill folder, where each may appear once
	a := &Archive{Files: make([]File, 0, len(entries))}
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		rel := strings.TrimPrefix(e.path, root)
		if seen[rel] {
			return nil, invalid("unsafe_path", fmt.Sprintf("contains %s twice", rel))
		}
		seen[rel] = true
		a.Size += int64(len(e.content))
		a.Files = append(a.Files, File{Path: rel, Mode: normalizeMode(e.mode, e.content), Content: e.content})
	}
	slices.SortFunc(a.Files, func(x, y File) int { return strings.Compare(x.Path, y.Path) })

	// A path can't be a file and a folder at once, since the sandbox couldn't create both
	for _, f := range a.Files {
		for dir := path.Dir(f.Path); dir != "."; dir = path.Dir(dir) {
			if seen[dir] {
				return nil, invalid("unsafe_path", fmt.Sprintf("contains %s both as a file and a folder", dir))
			}
		}
	}

	// The name and description come from the frontmatter of SKILL.md
	i := slices.IndexFunc(a.Files, func(f File) bool { return f.Path == skillFile })
	a.Name, a.Description, err = parseFrontmatter(a.Files[i].Content)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// isJunk reports whether an entry is metadata an archiver added rather than part of the skill
func isJunk(name string) bool {
	return name == "__MACOSX" || strings.HasPrefix(name, "__MACOSX/") || path.Base(name) == ".DS_Store"
}

// skillRoot returns the prefix to strip from every path, which is empty when SKILL.md sits at the root of the zip
func skillRoot(paths []string) (string, error) {
	if slices.Contains(paths, skillFile) {
		return "", nil
	}
	missing := invalid("missing_skill_md", "must contain a SKILL.md at its root or inside a single top-level folder")
	if len(paths) == 0 {
		return "", missing
	}
	top, _, ok := strings.Cut(paths[0], "/")
	if !ok {
		return "", missing
	}
	prefix := top + "/"
	for _, p := range paths {
		if !strings.HasPrefix(p, prefix) {
			return "", missing
		}
	}
	if !slices.Contains(paths, prefix+skillFile) {
		return "", missing
	}
	return prefix, nil
}

// readEntry reads one entry, failing once it grows past the remaining budget
func readEntry(f *zip.File, remaining int64) ([]byte, error) {
	rc, err := f.Open()
	if errors.Is(err, zip.ErrAlgorithm) {
		return nil, invalid("invalid_zip", "uses an unsupported compression method")
	} else if err != nil {
		return nil, invalid("invalid_zip", "is not a valid zip file")
	}
	defer rc.Close()
	content, err := io.ReadAll(io.LimitReader(rc, remaining+1))
	if err != nil {
		return nil, invalid("invalid_zip", "is not a valid zip file")
	}
	if int64(len(content)) > remaining {
		return nil, invalid("too_large", fmt.Sprintf("must unpack to at most %d MiB", maxUnpackedBytes>>20))
	}
	return content, nil
}

// normalizeMode keeps only whether a file is executable, which zips made on Windows don't record, so a shebang counts too
func normalizeMode(mode fs.FileMode, content []byte) fs.FileMode {
	if mode&0o111 != 0 || bytes.HasPrefix(content, []byte("#!")) {
		return 0o755
	}
	return 0o644
}

// parseFrontmatter reads the name and description from the YAML block at the start of SKILL.md
func parseFrontmatter(content []byte) (string, string, error) {
	text := strings.TrimPrefix(string(content), "\uFEFF")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	missing := invalid("invalid_frontmatter", "must start SKILL.md with a frontmatter block between --- lines that sets name and description")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return "", "", missing
	}
	end := strings.Index(rest, "\n---")
	if end < 0 {
		// An empty frontmatter block closes right after the opening line
		if !strings.HasPrefix(rest, "---") {
			return "", "", missing
		}
		end = 0
	}
	if end > maxFrontmatterBytes {
		return "", "", invalid("invalid_frontmatter", "has a SKILL.md frontmatter that is too long")
	}

	var meta struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(rest[:end]), &meta); err != nil {
		return "", "", invalid("invalid_frontmatter", "has a SKILL.md frontmatter that is not valid YAML")
	}

	name := strings.TrimSpace(meta.Name)
	if name == "" {
		return "", "", invalid("invalid_name", "must set name in the SKILL.md frontmatter")
	}
	if len(name) > maxNameLength || !nameRe.MatchString(name) {
		return "", "", invalid("invalid_name", fmt.Sprintf("has the name %q, but a skill name may only contain lowercase letters, digits and single hyphens, up to %d characters", name, maxNameLength))
	}
	description := strings.TrimSpace(meta.Description)
	switch {
	case description == "":
		return "", "", invalid("invalid_description", "must set description in the SKILL.md frontmatter")
	case len([]rune(description)) > maxDescriptionChars:
		return "", "", invalid("invalid_description", fmt.Sprintf("has a description longer than %d characters", maxDescriptionChars))
	case tagRe.MatchString(description):
		return "", "", invalid("invalid_description", "has a description with XML tags, which aren't allowed")
	}
	return name, description, nil
}

// Hash identifies the skill's content, independent of how the zip was compressed
func (a *Archive) Hash() string {
	h := sha256.New()
	for _, f := range a.Files {
		sum := sha256.Sum256(f.Content)
		fmt.Fprintf(h, "%s\x00%o\x00%x\n", f.Path, f.Mode, sum)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Zip writes the skill as a normalized zip, with sorted entries and fixed timestamps
func (a *Archive) Zip() ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range a.Files {
		hdr := &zip.FileHeader{Name: f.Path, Method: zip.Deflate, Modified: normalizedTime}
		hdr.SetMode(f.Mode)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(f.Content); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

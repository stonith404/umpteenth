//go:build unit

package skills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/egress"
)

func TestParseGitHubLink(t *testing.T) {
	tests := []struct {
		link       string
		ok         bool
		candidates []refDir
	}{
		{"https://github.com/acme/skills", true, []refDir{{ref: "HEAD"}}},
		{"https://github.com/acme/skills.git", true, []refDir{{ref: "HEAD"}}},
		{"https://github.com/acme/skills/tree/main", true, []refDir{{ref: "main"}}},
		{"https://github.com/acme/skills/tree/main/skills/pdf/", true, []refDir{{"main", "skills/pdf"}, {"main/skills", "pdf"}, {"main/skills/pdf", ""}}},
		{"https://github.com/acme/skills/blob/main/skills/pdf/SKILL.md", true, []refDir{{"main", "skills/pdf"}, {"main/skills", "pdf"}, {"main/skills/pdf", ""}}},
		{"https://www.github.com/acme/skills/tree/v1", true, []refDir{{ref: "v1"}}},
		{"https://github.com/acme/skills/releases/download/v1/pdf.skill", false, nil},
		{"https://github.com/acme", false, nil},
		{"https://github.com/acme/skills/blob/main", false, nil},
		{"https://github.com/ac%2Fme/skills", false, nil},
		{"https://example.com/acme/skills/tree/main", false, nil},
	}
	for _, tt := range tests {
		u, err := url.Parse(tt.link)
		require.NoError(t, err)
		link, ok := parseGitHubLink(u)
		require.Equal(t, tt.ok, ok, tt.link)
		if ok {
			require.Equal(t, tt.candidates, link.candidates, tt.link)
		}
	}
}

// tarEntry is one entry of a fake repository archive, a symlink when link is set
type tarEntry struct {
	name    string
	content string
	mode    int64
	link    string
}

// repoArchive builds a tarball the way GitHub does, with everything inside one folder named after the repository and ref
func repoArchive(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header", PAXRecords: map[string]string{"comment": "abc"}}))
	require.NoError(t, tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: "skills-main/", Mode: 0o755}))
	for _, e := range entries {
		hdr := &tar.Header{Typeflag: tar.TypeReg, Name: "skills-main/" + e.name, Mode: e.mode, Size: int64(len(e.content))}
		if hdr.Mode == 0 {
			hdr.Mode = 0o644
		}
		if e.link != "" {
			hdr.Typeflag, hdr.Linkname, hdr.Size = tar.TypeSymlink, e.link, 0
		}
		require.NoError(t, tw.WriteHeader(hdr))
		if e.link == "" {
			_, err := tw.Write([]byte(e.content))
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

// fakeGitHub serves one archive for one ref, answers 404 for any other, and records the paths it was asked for
type fakeGitHub struct {
	*httptest.Server
	mu        sync.Mutex
	requested []string
}

func newFakeGitHub(t *testing.T, ref string, archive []byte) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requested = append(f.requested, r.URL.Path)
		f.mu.Unlock()
		if r.URL.Path != "/acme/skills/tar.gz/"+ref {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(archive)
	}))
	t.Cleanup(f.Close)
	return f
}

func skillMD(name, description string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\n---\nBody\n"
}

func fetchLink(t *testing.T, f *fakeGitHub, link string) (*Archive, error) {
	t.Helper()
	m := &Module{deps: Dependencies{Egress: egress.New(true)}, githubArchives: f.URL}
	u, err := url.Parse(link)
	require.NoError(t, err)
	parsed, ok := parseGitHubLink(u)
	require.True(t, ok)
	var a *Archive
	err = m.downloadRepo(context.Background(), m.deps.Egress.HTTPClient(importTimeout), parsed, func(body io.Reader, _ refDir, dir string) error {
		a, err = extractFolder(body, dir)
		return err
	})
	return a, err
}

func TestFetchGitHubFolderKeepsOnlyTheLinkedFolder(t *testing.T) {
	// The branch name has a slash, so the shorter readings of the link come first and answer 404
	f := newFakeGitHub(t, "feature/pdf", repoArchive(t,
		tarEntry{name: "README.md", content: "repo"},
		tarEntry{name: "skills/pdf/SKILL.md", content: skillMD("pdf", "Fills PDF forms")},
		tarEntry{name: "skills/pdf/scripts/fill.py", content: "print(1)", mode: 0o755},
		tarEntry{name: "skills/pdf/.DS_Store", content: "junk"},
		tarEntry{name: "skills/docx/SKILL.md", content: skillMD("docx", "Edits Word files")},
	))
	a, err := fetchLink(t, f, "https://github.com/acme/skills/tree/feature/pdf/skills/pdf")
	require.NoError(t, err)
	require.Equal(t, "pdf", a.Name)
	require.Equal(t, []string{"SKILL.md", "scripts/fill.py"}, paths(a))
	require.True(t, a.Files[1].Executable())
	require.Equal(t, []string{"/acme/skills/tar.gz/feature", "/acme/skills/tar.gz/feature/pdf"}, f.requested)
}

func TestFetchGitHubFolderNeedsOneSkill(t *testing.T) {
	archive := repoArchive(t,
		tarEntry{name: "README.md", content: "repo"},
		tarEntry{name: "skills/pdf/SKILL.md", content: skillMD("pdf", "Fills PDF forms")},
		tarEntry{name: "skills/docx/SKILL.md", content: skillMD("docx", "Edits Word files")},
		tarEntry{name: "skills/xlsx/SKILL.md", content: skillMD("xlsx", "Edits spreadsheets")},
		tarEntry{name: "skills/pptx/SKILL.md", content: skillMD("pptx", "Edits slides")},
	)
	f := newFakeGitHub(t, "HEAD", archive)

	// A repository with several skills names some of them
	_, err := fetchLink(t, f, "https://github.com/acme/skills")
	require.Error(t, err)
	require.Equal(t, "several_skills", codeOf(t, err))
	require.Contains(t, err.Error(), "points at a folder with 4 skills, such as skills/docx, skills/pdf, skills/pptx")

	// A folder that isn't in the repository
	f = newFakeGitHub(t, "main", archive)
	_, err = fetchLink(t, f, "https://github.com/acme/skills/tree/main/skills/missing")
	require.Equal(t, "not_found", codeOf(t, err))

	// A branch that doesn't exist
	_, err = fetchLink(t, f, "https://github.com/acme/skills/tree/nope")
	require.Equal(t, "not_found", codeOf(t, err))
}

func TestFetchGitHubFolderTakesASingleSkillFurtherDown(t *testing.T) {
	// A repository with a README next to its only skill works from its own page
	f := newFakeGitHub(t, "HEAD", repoArchive(t,
		tarEntry{name: "README.md", content: "repo"},
		tarEntry{name: "LICENSE", content: "MIT"},
		tarEntry{name: "pdf/SKILL.md", content: skillMD("pdf", "Fills PDF forms")},
		tarEntry{name: "pdf/reference.md", content: "notes"},
	))
	a, err := fetchLink(t, f, "https://github.com/acme/skills")
	require.NoError(t, err)
	require.Equal(t, []string{"SKILL.md", "reference.md"}, paths(a))
}

func TestFetchGitHubFolderRefusesSymlinks(t *testing.T) {
	f := newFakeGitHub(t, "main", repoArchive(t,
		tarEntry{name: "pdf/SKILL.md", content: skillMD("pdf", "Fills PDF forms")},
		tarEntry{name: "pdf/secrets", link: "/etc/passwd"},
	))
	_, err := fetchLink(t, f, "https://github.com/acme/skills/tree/main/pdf")
	require.Equal(t, "unsafe_path", codeOf(t, err))
}

func TestImportFromZipLink(t *testing.T) {
	zipBody := skillZip(t, "demo", "Says hello")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "demo.skill") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(zipBody)
	}))
	t.Cleanup(srv.Close)
	m := &Module{deps: Dependencies{Egress: egress.New(true)}, githubArchives: defaultGitHubArchives}

	a, err := m.importFrom(context.Background(), srv.URL+"/files/demo.skill")
	require.NoError(t, err)
	require.Equal(t, "demo", a.Name)

	// Problems with the download are reported on the url field
	_, err = m.importFrom(context.Background(), srv.URL+"/files/other.zip")
	requireInvalidField(t, err, "url", "unreachable")
	require.Contains(t, err.Error(), "answered with HTTP 404")

	// A guard that refuses private addresses keeps the download from reaching the local server
	m.deps.Egress = egress.New(false)
	_, err = m.importFrom(context.Background(), srv.URL+"/files/demo.skill")
	requireInvalidField(t, err, "url", "forbidden")
}

func TestReimportFollowsTheSourceLink(t *testing.T) {
	h := newHarness(t)
	var mu sync.Mutex
	description := "First"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = w.Write(skillZip(t, "demo", description))
	}))
	t.Cleanup(srv.Close)
	h.m.deps.Egress = egress.New(true)
	link := srv.URL + "/demo.zip"

	// An imported skill keeps its link
	in := &importInput{}
	in.Body.URL = " " + link + " "
	out, err := h.m.importSkill(h.ctx, in)
	require.NoError(t, err)
	require.Len(t, out.Body.Skills, 1)
	skill := out.Body.Skills[0]
	require.Equal(t, link, *skill.SourceURL)

	// Importing again without a link takes the new version from the same one
	mu.Lock()
	description = "Second"
	mu.Unlock()
	again, err := h.m.reimportSkill(h.ctx, &reimportInput{ID: skill.ID})
	require.NoError(t, err)
	require.Equal(t, "Second", again.Body.Description)
	require.Equal(t, link, *again.Body.SourceURL)

	// An uploaded version drops the link, since the skill no longer matches it
	replaced, err := h.m.replace(h.ctx, &replaceInput{ID: skill.ID, RawBody: skillZip(t, "demo", "Third")})
	require.NoError(t, err)
	require.Nil(t, replaced.Body.SourceURL)
	_, err = h.m.reimportSkill(h.ctx, &reimportInput{ID: skill.ID})
	requireInvalidField(t, err, "url", "required")

	// The same content from a link brings the link back without a new version
	mu.Lock()
	description = "Third"
	mu.Unlock()
	re := &reimportInput{ID: skill.ID}
	re.Body.URL = link
	back, err := h.m.reimportSkill(h.ctx, re)
	require.NoError(t, err)
	require.Equal(t, replaced.Body.ContentHash, back.Body.ContentHash)
	require.Equal(t, link, *back.Body.SourceURL)
}

// codeOf is the code of a problem with a skill's content
func codeOf(t *testing.T, err error) string {
	t.Helper()
	var ae *archiveError
	require.ErrorAs(t, err, &ae)
	return ae.code
}

// pickRepo is a repository with several skills, one of them broken
func pickRepo(t *testing.T) []byte {
	return repoArchive(t,
		tarEntry{name: "README.md", content: "repo"},
		tarEntry{name: "skills/pdf/SKILL.md", content: skillMD("pdf", "Fills PDF forms")},
		tarEntry{name: "skills/pdf/scripts/fill.py", content: "print(1)", mode: 0o755},
		tarEntry{name: "skills/docx/SKILL.md", content: skillMD("docx", "Edits Word files")},
		tarEntry{name: "skills/docx/reference.md", content: "notes"},
		tarEntry{name: "skills/broken/SKILL.md", content: "---\nname: broken\n---\n"},
	)
}

func TestPreviewListsTheSkillsOfAFolder(t *testing.T) {
	h := newHarness(t)
	f := newFakeGitHub(t, "main", pickRepo(t))
	h.m.deps.Egress, h.m.githubArchives = egress.New(true), f.URL
	h.create(t, skillZip(t, "docx", "Already here"))

	in := &previewInput{}
	in.Body.URL = "https://github.com/acme/skills/tree/main/skills"
	out, err := h.m.previewImport(h.ctx, in)
	require.NoError(t, err)

	// Every skill is listed with a link to its own folder, the workspace's own marked and the broken one explained
	require.Equal(t, []SkillChoice{
		{Path: "broken", Name: "broken", URL: "https://github.com/acme/skills/tree/main/skills/broken", Problem: "It must set description in the SKILL.md frontmatter"},
		{Path: "docx", Name: "docx", Description: "Edits Word files", URL: "https://github.com/acme/skills/tree/main/skills/docx", Exists: true},
		{Path: "pdf", Name: "pdf", Description: "Fills PDF forms", URL: "https://github.com/acme/skills/tree/main/skills/pdf"},
	}, out.Body.Skills)

	// A link that isn't a GitHub folder has nothing to list
	in.Body.URL = "https://example.com/pdf.skill"
	_, err = h.m.previewImport(h.ctx, in)
	requireInvalidField(t, err, "url", "not_a_folder")
}

func TestImportAddsThePickedSkills(t *testing.T) {
	h := newHarness(t)
	f := newFakeGitHub(t, "HEAD", pickRepo(t))
	h.m.deps.Egress, h.m.githubArchives = egress.New(true), f.URL

	// Two picked skills come from one download, each with the link to its own folder
	in := &importInput{}
	in.Body.URL = "https://github.com/acme/skills"
	in.Body.Paths = []string{"skills/pdf", "skills/docx/", "skills/pdf"}
	out, err := h.m.importSkill(h.ctx, in)
	require.NoError(t, err)
	require.Len(t, out.Body.Skills, 2)
	require.Equal(t, "pdf", out.Body.Skills[0].Name)
	require.Equal(t, []SkillFile{{Path: "SKILL.md", Size: out.Body.Skills[0].Files[0].Size}, {Path: "scripts/fill.py", Size: 8, Executable: true}}, out.Body.Skills[0].Files)
	require.Equal(t, "https://github.com/acme/skills/tree/HEAD/skills/pdf", *out.Body.Skills[0].SourceURL)
	require.Equal(t, "docx", out.Body.Skills[1].Name)
	f.mu.Lock()
	require.Equal(t, []string{"/acme/skills/tar.gz/HEAD"}, f.requested)
	f.mu.Unlock()

	// A skill the workspace has, a broken one and a folder without a skill each refuse the whole pick
	before := len(h.blobs(t, ""))
	in.Body.Paths = []string{"skills/pdf"}
	_, err = h.m.importSkill(h.ctx, in)
	requireInvalidField(t, err, "paths", "already_added")
	in.Body.Paths = []string{"skills/broken"}
	_, err = h.m.importSkill(h.ctx, in)
	requireInvalidField(t, err, "url", "invalid_description")
	require.Contains(t, err.Error(), "(in skills/broken)")
	in.Body.Paths = []string{"README.md"}
	_, err = h.m.importSkill(h.ctx, in)
	requireInvalidField(t, err, "url", "not_found")
	in.Body.Paths = []string{"../etc"}
	_, err = h.m.importSkill(h.ctx, in)
	requireInvalidField(t, err, "paths", "invalid")
	require.Len(t, h.blobs(t, ""), before)

	// Each picked skill updates from its own folder
	again, err := h.m.reimportSkill(h.ctx, &reimportInput{ID: out.Body.Skills[1].ID})
	require.NoError(t, err)
	require.Equal(t, out.Body.Skills[1].ContentHash, again.Body.ContentHash)
}

//go:build unit

package skills

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
)

// entry is one file of a test zip
type entry struct {
	name    string
	content string
	mode    fs.FileMode
}

func makeZip(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			hdr.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(hdr)
		require.NoError(t, err)
		_, err = w.Write([]byte(e.content))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

const demoSkillMD = "---\nname: demo\ndescription: Says hello when a job needs a greeting\nlicense: MIT\n---\n# Demo\nRun scripts/hello.sh.\n"

// requireInvalid checks that parsing failed on the file field with the given code
func requireInvalid(t *testing.T, err error, code string) {
	t.Helper()
	appErr, ok := apperror.As(err)
	require.True(t, ok, err)
	raw, err := appErr.MarshalJSON()
	require.NoError(t, err)
	var body apperror.Body
	require.NoError(t, json.Unmarshal(raw, &body))
	require.Len(t, body.Fields, 1, appErr.Error())
	require.Equal(t, "file", body.Fields[0].Field)
	require.Equal(t, code, body.Fields[0].Code, appErr.Error())
}

func TestParseArchiveWithFilesAtTheRoot(t *testing.T) {
	a, err := ParseArchive(makeZip(t,
		entry{name: "SKILL.md", content: demoSkillMD},
		entry{name: "scripts/hello.sh", content: "echo hello\n", mode: 0o755},
		entry{name: "reference.md", content: "notes"},
	))
	require.NoError(t, err)
	require.Equal(t, "demo", a.Name)
	require.Equal(t, "Says hello when a job needs a greeting", a.Description)

	// Files are sorted, and only the exec bit of a mode survives
	require.Equal(t, []string{"SKILL.md", "reference.md", "scripts/hello.sh"}, paths(a))
	require.Equal(t, fs.FileMode(0o644), a.Files[0].Mode)
	require.Equal(t, fs.FileMode(0o755), a.Files[2].Mode)
	require.Equal(t, int64(len(demoSkillMD)+len("echo hello\n")+len("notes")), a.Size)
}

func TestParseArchiveStripsASingleTopLevelFolder(t *testing.T) {
	// macOS Finder adds __MACOSX and .DS_Store next to the folder it compresses
	a, err := ParseArchive(makeZip(t,
		entry{name: "demo/", mode: fs.ModeDir | 0o755},
		entry{name: "demo/SKILL.md", content: demoSkillMD},
		entry{name: "demo/scripts/run.py", content: "#!/usr/bin/env python3\nprint(1)\n"},
		entry{name: "demo/.DS_Store", content: "junk"},
		entry{name: "__MACOSX/demo/._SKILL.md", content: "junk"},
	))
	require.NoError(t, err)
	require.Equal(t, []string{"SKILL.md", "scripts/run.py"}, paths(a))

	// A zip made on Windows carries no modes, so a shebang marks a script as executable
	require.True(t, a.Files[1].Executable())
}

func TestParseArchiveAcceptsWindowsPaths(t *testing.T) {
	a, err := ParseArchive(makeZip(t, entry{name: `demo\SKILL.md`, content: demoSkillMD}, entry{name: `demo\docs\a.md`, content: "a"}))
	require.NoError(t, err)
	require.Equal(t, []string{"SKILL.md", "docs/a.md"}, paths(a))
}

func TestParseArchiveNeedsSKILLmdInOnePlace(t *testing.T) {
	tests := map[string][]entry{
		"no SKILL.md":         {{name: "README.md", content: "x"}},
		"two top folders":     {{name: "a/SKILL.md", content: demoSkillMD}, {name: "b/SKILL.md", content: demoSkillMD}},
		"nested too deep":     {{name: "a/b/SKILL.md", content: demoSkillMD}},
		"file next to folder": {{name: "demo/SKILL.md", content: demoSkillMD}, {name: "stray.txt", content: "x"}},
		"empty":               {},
	}
	for name, entries := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ParseArchive(makeZip(t, entries...))
			requireInvalid(t, err, "missing_skill_md")
		})
	}
}

func TestParseArchiveRejectsUnsafeEntries(t *testing.T) {
	tests := map[string][]entry{
		"parent path":     {{name: "SKILL.md", content: demoSkillMD}, {name: "../evil.sh", content: "x"}},
		"absolute path":   {{name: "SKILL.md", content: demoSkillMD}, {name: "/etc/passwd", content: "x"}},
		"windows parent":  {{name: "SKILL.md", content: demoSkillMD}, {name: `..\evil.sh`, content: "x"}},
		"symlink":         {{name: "SKILL.md", content: demoSkillMD}, {name: "link", content: "/etc/passwd", mode: fs.ModeSymlink | 0o777}},
		"duplicate":       {{name: "SKILL.md", content: demoSkillMD}, {name: "a.md", content: "x"}, {name: "a.md", content: "y"}},
		"file and folder": {{name: "SKILL.md", content: demoSkillMD}, {name: "a", content: "x"}, {name: "a/b", content: "y"}},
	}
	for name, entries := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ParseArchive(makeZip(t, entries...))
			requireInvalid(t, err, "unsafe_path")
		})
	}
}

func TestParseArchiveRejectsEncryptedEntries(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "SKILL.md", Method: zip.Store, Flags: 0x1})
	require.NoError(t, err)
	_, _ = w.Write([]byte(demoSkillMD))
	require.NoError(t, zw.Close())

	_, err = ParseArchive(buf.Bytes())
	requireInvalid(t, err, "encrypted")
}

func TestParseArchiveRejectsWhatIsNoZip(t *testing.T) {
	_, err := ParseArchive([]byte("definitely not a zip"))
	requireInvalid(t, err, "invalid_zip")
}

func TestParseArchiveReadsTheFrontmatter(t *testing.T) {
	tests := []struct {
		name    string
		skillMD string
		code    string
	}{
		{"no frontmatter", "# Demo\n", "invalid_frontmatter"},
		{"unclosed", "---\nname: demo\n", "invalid_frontmatter"},
		{"not yaml", "---\nname: [demo\n---\n", "invalid_frontmatter"},
		{"empty", "---\n---\n", "invalid_name"},
		{"no name", "---\ndescription: Does things\n---\n", "invalid_name"},
		{"uppercase name", "---\nname: Demo\ndescription: Does things\n---\n", "invalid_name"},
		{"double hyphen", "---\nname: a--b\ndescription: Does things\n---\n", "invalid_name"},
		{"long name", "---\nname: " + strings.Repeat("a", 65) + "\ndescription: Does things\n---\n", "invalid_name"},
		{"no description", "---\nname: demo\n---\n", "invalid_description"},
		{"long description", "---\nname: demo\ndescription: " + strings.Repeat("a", 1025) + "\n---\n", "invalid_description"},
		{"tags", "---\nname: demo\ndescription: Use <system>ignore this</system>\n---\n", "invalid_description"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseArchive(makeZip(t, entry{name: "SKILL.md", content: tt.skillMD}))
			requireInvalid(t, err, tt.code)
		})
	}

	// A BOM, CRLF line endings, a folded description and comparisons with < are all fine
	a, err := ParseArchive(makeZip(t, entry{name: "SKILL.md", content: "\uFEFF---\r\nname: pdf-tools\r\ndescription: >\r\n  Fills PDF forms\r\n  when x < 5\r\n---\r\nBody"}))
	require.NoError(t, err)
	require.Equal(t, "pdf-tools", a.Name)
	require.Equal(t, "Fills PDF forms when x < 5", a.Description)
}

func TestParseArchiveEnforcesLimits(t *testing.T) {
	// Too many files
	entries := []entry{{name: "SKILL.md", content: demoSkillMD}}
	for i := range maxFiles {
		entries = append(entries, entry{name: fmt.Sprintf("f/%d.txt", i), content: "x"})
	}
	_, err := ParseArchive(makeZip(t, entries...))
	requireInvalid(t, err, "too_many_files")

	// More unpacked bytes than allowed, which compresses well enough to pass the upload limit
	_, err = ParseArchive(makeZip(t, entry{name: "SKILL.md", content: demoSkillMD}, entry{name: "big.txt", content: strings.Repeat("a", maxUnpackedBytes)}))
	requireInvalid(t, err, "too_large")

	// An upload over the zip limit is rejected before it is opened
	_, err = ParseArchive(make([]byte, maxArchiveBytes+1))
	requireInvalid(t, err, "too_large")
}

func TestParseArchiveCountsTheBytesActuallyRead(t *testing.T) {
	// A header that understates the size can't get more than the declared size past the reader
	big := bytes.Repeat([]byte("a"), maxUnpackedBytes+10)
	var deflated bytes.Buffer
	fw, err := flate.NewWriter(&deflated, flate.BestCompression)
	require.NoError(t, err)
	_, _ = fw.Write(big)
	require.NoError(t, fw.Close())

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("SKILL.md")
	require.NoError(t, err)
	_, _ = w.Write([]byte(demoSkillMD))
	rw, err := zw.CreateRaw(&zip.FileHeader{
		Name: "big.txt", Method: zip.Deflate, CRC32: crc32.ChecksumIEEE(big),
		CompressedSize64: uint64(deflated.Len()), UncompressedSize64: 10, // #nosec G115 -- a buffer length is never negative
	})
	require.NoError(t, err)
	_, _ = rw.Write(deflated.Bytes())
	require.NoError(t, zw.Close())

	_, err = ParseArchive(buf.Bytes())
	require.Error(t, err)
}

func TestHashAndZipAreStable(t *testing.T) {
	a1, err := ParseArchive(makeZip(t, entry{name: "SKILL.md", content: demoSkillMD}, entry{name: "b.md", content: "b"}, entry{name: "a.sh", content: "x", mode: 0o755}))
	require.NoError(t, err)
	a2, err := ParseArchive(makeZip(t, entry{name: "demo/a.sh", content: "x", mode: 0o700}, entry{name: "demo/b.md", content: "b"}, entry{name: "demo/SKILL.md", content: demoSkillMD}))
	require.NoError(t, err)

	// The same files in another order, folder or compression make the same version
	require.Equal(t, a1.Hash(), a2.Hash())
	z1, err := a1.Zip()
	require.NoError(t, err)
	z2, err := a2.Zip()
	require.NoError(t, err)
	require.Equal(t, z1, z2)

	// The normalized zip parses back to the same skill, modes included
	back, err := ParseArchive(z1)
	require.NoError(t, err)
	require.Equal(t, a1, back)

	// A changed mode is a new version
	a3, err := ParseArchive(makeZip(t, entry{name: "SKILL.md", content: demoSkillMD}, entry{name: "b.md", content: "b"}, entry{name: "a.sh", content: "x"}))
	require.NoError(t, err)
	require.NotEqual(t, a1.Hash(), a3.Hash())
}

func paths(a *Archive) []string {
	out := make([]string, len(a.Files))
	for i, f := range a.Files {
		out[i] = f.Path
	}
	return out
}

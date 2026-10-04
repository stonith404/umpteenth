//go:build unit

package dockerfile

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckRejectsAdd(t *testing.T) {
	require.NoError(t, Check("FROM debian:trixie-slim\nRUN curl -fsSL https://example.com/tool -o /usr/local/bin/tool\nCOPY --from=ghcr.io/astral-sh/uv:latest /uv /bin/\n"))

	rejected := map[string]string{
		"url":                   "FROM debian\nADD https://example.com/file /file\n",
		"lowercase":             "FROM debian\nadd https://example.com/file /file\n",
		"split over lines":      "FROM debian\nAD\\\nD https://example.com/file /file\n",
		"other escape":          "# escape=`\nFROM debian\nRUN echo \\\nADD https://example.com/file /file\n",
		"deferred with onbuild": "FROM debian AS base\nONBUILD ADD https://example.com/file /file\nFROM base\n",
	}
	for name, text := range rejected {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, Check(text), ErrAdd)
		})
	}
}

func TestCheckTriggers(t *testing.T) {
	require.NoError(t, CheckTriggers([]string{"RUN echo hi", "COPY . /app"}))
	require.ErrorIs(t, CheckTriggers([]string{"RUN echo hi", "ADD https://example.com/file /file"}), ErrAdd)
}

func TestCopyImagesLeavesOutStages(t *testing.T) {
	text := "FROM debian AS build\nRUN echo build\nFROM debian\nCOPY --from=build /out /out\nCOPY --from=0 /out /out\nCOPY --from=ghcr.io/astral-sh/uv:latest /uv /bin/\nCOPY a b\n"
	images, err := CopyImages(text)
	require.NoError(t, err)
	assert.Equal(t, []string{"ghcr.io/astral-sh/uv:latest"}, images)
}

func TestBaseImagesLeavesOutStagesAndScratch(t *testing.T) {
	text := "ARG BASE=python:3.13-slim\nFROM ${BASE} AS build\nRUN echo build\nFROM --platform=linux/arm64 debian:trixie-slim\nCOPY --from=build /out /out\nFROM build\nFROM scratch\n"
	bases, err := BaseImages(text)
	require.NoError(t, err)
	assert.Equal(t, []Base{{Ref: "python:3.13-slim"}, {Ref: "debian:trixie-slim", Platform: "linux/arm64"}}, bases)
}

func TestCheckRejectsUnenumeratedImageSources(t *testing.T) {
	for _, text := range []string{
		"FROM alpine AS base\nONBUILD COPY --from=blocked.example/private /x /x\nFROM base\n",
		"FROM alpine\nRUN --mount=from=blocked.example/private,target=/x cat /x/file\n",
		"# syntax=attacker.example/frontend\nFROM alpine\n",
	} {
		require.Error(t, Check(text), text)
	}
	require.Error(t, CheckTriggers([]string{"COPY --from=blocked.example/private /x /x"}))
	require.Error(t, CheckTriggers([]string{"RUN --mount=from=blocked.example/private,target=/x cat /x/file"}))
	require.NoError(t, Check("FROM alpine\nRUN --mount=type=cache,target=/cache echo hi\n"))
}

func TestRewriteSourcesPreservesStagesAndHeredocs(t *testing.T) {
	text := `ARG BASE=alpine:3
FROM $BASE AS base
RUN <<EOF
echo unchanged
EOF
FROM base
COPY --from=0 /a /a
COPY \
 --from=registry.example/tool:1 /bin/tool /bin/tool
`

	var refs []string
	rewritten, err := RewriteSources(text, func(base Base) (string, error) {
		refs = append(refs, base.Ref)
		return "sha256:" + strings.Repeat("a", 64), nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{"alpine:3", "registry.example/tool:1"}, refs)
	require.Contains(t, rewritten, "FROM base")
	require.Contains(t, rewritten, "COPY --from=0 /a /a")
	require.Contains(t, rewritten, "RUN <<EOF\necho unchanged\nEOF")
	require.NotContains(t, rewritten, "--from=registry.example")
	require.NoError(t, Check(rewritten))
}

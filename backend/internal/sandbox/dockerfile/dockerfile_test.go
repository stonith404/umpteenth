//go:build unit

package dockerfile

import (
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

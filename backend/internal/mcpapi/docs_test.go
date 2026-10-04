//go:build unit

package mcpapi

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// docsFixture is a docs directory with what the real pages hold: frontmatter, components, asides, tabs, links between pages and a code block with a # comment
var docsFixture = fstest.MapFS{
	"docs/.gitkeep": {},
	"docs/guides/triggers.mdx": {Data: []byte(`---
title: Triggers and schedules
description: Start runs by hand, on a schedule or from a webhook.
---

import { Steps } from '@astrojs/starlight/components';
import Screenshot from '../../../components/Screenshot.astro';

Read [Runs](../runs/) and [the overlap rules](#overlapping-runs) first.

<Screenshot
  name="schedule"
  alt="The schedule card"
/>

## Schedules

A cron expression starts runs, see [Cron](https://crontab.guru).

` + "```yaml\n# not a heading\ncron: \"0 8 * * 1-5\"\n```" + `

:::caution[Time zones]
Umpteenth evaluates the expression in UTC unless you pick a zone.
:::

### Time zones

<Steps>

1. Pick an IANA zone such as Europe/Berlin.

</Steps>

## Behind a proxy

<Tabs syncKey="proxy">
<TabItem label="Caddy">

Caddy needs nothing extra.

</TabItem>
</Tabs>
`)},
	"docs/guides/sandboxes.md": {Data: []byte(`---
title: Sandboxes
description: "Each run gets a disposable sandbox: network, secrets and files."
---

## Network

An allowlist job reaches only the domains it lists.
`)},
}

func TestLoadDocsTurnsPagesIntoSections(t *testing.T) {
	sections, err := loadDocs(docsFixture)
	require.NoError(t, err)

	// Pages come in path order, each with its intro and a section per heading, under the URL the website gives them
	var headings, urls []string
	for _, s := range sections {
		headings = append(headings, s.Heading)
		urls = append(urls, s.URL)
	}
	assert.Equal(t, []string{
		"Sandboxes",
		"Sandboxes › Network",
		"Triggers and schedules",
		"Triggers and schedules › Schedules",
		"Triggers and schedules › Schedules › Time zones",
		"Triggers and schedules › Behind a proxy",
	}, headings)
	assert.Equal(t, []string{
		"https://umpteenth.dev/guides/sandboxes/",
		"https://umpteenth.dev/guides/sandboxes/#network",
		"https://umpteenth.dev/guides/triggers/",
		"https://umpteenth.dev/guides/triggers/#schedules",
		"https://umpteenth.dev/guides/triggers/#time-zones",
		"https://umpteenth.dev/guides/triggers/#behind-a-proxy",
	}, urls)

	// The intro starts with the description, components and imports go away, and links point at the website
	assert.Equal(t, "> Start runs by hand, on a schedule or from a webhook.\n\nRead [Runs](https://umpteenth.dev/guides/runs/) and [the overlap rules](https://umpteenth.dev/guides/triggers/#overlapping-runs) first.", sections[2].Body)

	// Code blocks stay as they are, and an aside keeps its kind and title as a label
	assert.Equal(t, "A cron expression starts runs, see [Cron](https://crontab.guru).\n\n```yaml\n# not a heading\ncron: \"0 8 * * 1-5\"\n```\n\n**Caution: Time zones**\nUmpteenth evaluates the expression in UTC unless you pick a zone.", sections[3].Body)
	assert.Equal(t, "1. Pick an IANA zone such as Europe/Berlin.", sections[4].Body)
	assert.Equal(t, "**Caddy**\n\nCaddy needs nothing extra.", sections[5].Body)
}

func TestLoadDocsWithoutPagesFindsNothing(t *testing.T) {
	// Builds outside the image embed only the placeholder
	sections, err := loadDocs(fstest.MapFS{"docs/.gitkeep": {}})
	require.NoError(t, err)
	assert.Empty(t, sections)
}

func TestLoadDocsRefusesAPageWithoutATitle(t *testing.T) {
	_, err := loadDocs(fstest.MapFS{"docs/broken.md": {Data: []byte("---\ndescription: x\n---\n\nText\n")}})
	assert.ErrorContains(t, err, "docs/broken.md: the page has no title")
}

func newFixtureIndex(t *testing.T) *docsIndex {
	t.Helper()
	sections, err := loadDocs(docsFixture)
	require.NoError(t, err)
	index, err := newDocsIndex(t.Context(), sections)
	require.NoError(t, err)
	return index
}

func TestSearchDocsRanksHeadingsAndIgnoresQuerySyntax(t *testing.T) {
	index := newFixtureIndex(t)

	// A word in a heading ranks its section first, and the stemmer matches other forms of a word
	sections, err := index.search(t.Context(), "zone", 5)
	require.NoError(t, err)
	require.NotEmpty(t, sections)
	assert.Equal(t, "Triggers and schedules › Schedules › Time zones", sections[0].Heading)

	// Quotes, operators and other FTS5 syntax are only words
	sections, err = index.search(t.Context(), `"allowlist" NOT -caddy* (network`, 2)
	require.NoError(t, err)
	require.Len(t, sections, 2)
	assert.Equal(t, "Sandboxes › Network", sections[0].Heading)

	_, err = index.search(t.Context(), `"*" -`, 5)
	assert.EqualError(t, err, "The query needs at least one word")
}

// callSearchDocs calls the tool's handler and returns its text and whether it is an error
func callSearchDocs(t *testing.T, index *docsIndex, args string) (string, bool) {
	t.Helper()
	res, err := index.handler(t.Context(), &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "search_docs", Arguments: json.RawMessage(args)}})
	require.NoError(t, err)
	return res.Content[0].(*mcp.TextContent).Text, res.IsError
}

func TestSearchDocsToolChecksItsArguments(t *testing.T) {
	index := newFixtureIndex(t)

	text, isError := callSearchDocs(t, index, `{"query":"cron","limit":1}`)
	assert.False(t, isError)
	assert.True(t, strings.HasPrefix(text, "## Triggers and schedules › Schedules\nhttps://umpteenth.dev/guides/triggers/#schedules\n\nA cron expression"), text)

	for args, problem := range map[string]string{
		`{}`:                          `Missing required argument "query"`,
		`{"query":"cron","limit":11}`: "The limit must be between 1 and 10",
		`{"query":"cron","page":2}`:   "The arguments must be an object with a query and an optional limit",
		`{"query":"%%%"}`:             "The query needs at least one word",
	} {
		text, isError := callSearchDocs(t, index, args)
		assert.True(t, isError, args)
		assert.Equal(t, problem, text, args)
	}

	text, isError = callSearchDocs(t, index, `{"query":"kubernetes"}`)
	assert.False(t, isError)
	assert.Equal(t, "No section of the docs matches. Try other or fewer words.", text)
}

func TestSearchDocsWithoutPagesPointsAtTheWebsite(t *testing.T) {
	index, err := newDocsIndex(t.Context(), nil)
	require.NoError(t, err)
	text, isError := callSearchDocs(t, index, `{"query":"cron"}`)
	assert.False(t, isError)
	assert.Equal(t, "This build of Umpteenth doesn't include its docs, read them at https://umpteenth.dev/ instead.", text)
}

func TestTruncateSectionCutsOnACharacterBoundary(t *testing.T) {
	body := strings.Repeat("a", maxSectionChars-1) + "ü" + "rest"
	cut := truncateSection(body)
	assert.True(t, strings.HasPrefix(cut, strings.Repeat("a", maxSectionChars-1)+"\n\n[The section continues"), "the two-byte ü that straddles the limit is dropped whole")
	assert.Equal(t, "short", truncateSection("short"))
}

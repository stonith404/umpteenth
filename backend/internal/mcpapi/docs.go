package mcpapi

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.yaml.in/yaml/v3"

	// The docs index is an in-memory SQLite database with full-text search, whatever engine the app itself stores its data in
	_ "modernc.org/sqlite"
)

// docsFS holds the pages of the docs site, which the image build copies into docs, see docker/Dockerfile
// Other builds embed only the placeholder, and search_docs then points the agent at the website
//
//go:embed all:docs
var docsFS embed.FS

// docsSite is where the docs are published, which page URLs and the links between pages resolve against
const docsSite = "https://umpteenth.dev/"

// docSection is a part of a docs page under one heading, the unit a search returns
type docSection struct {
	Page    string
	Heading string
	URL     string
	Body    string
}

const (
	// maxDocsQuery bounds the keywords of a search, which never needs more than a few
	maxDocsQuery = 200
	// defaultDocsResults and maxDocsResults are how many sections a search returns, which with sections of a few KB keeps a result small
	defaultDocsResults = 5
	maxDocsResults     = 10
	// maxSectionChars cuts a section that grows unusually long, so one result never floods the agent's context
	maxSectionChars = 6000
)

var (
	// markdownLink is a Markdown link or image, whose relative target resolves against the page's URL
	markdownLink = regexp.MustCompile(`(!?\[[^\]]*\])\(([^)\s]+)\)`)
	// tabLabel is the label of a tab the docs show next to others, such as one reverse proxy's config
	tabLabel = regexp.MustCompile(`^<TabItem\s+label="([^"]+)"`)
	// queryWord is a word of a search, since FTS5 would read quotes, minus signs and operators as query syntax
	queryWord = regexp.MustCompile(`[\p{L}\p{N}_]+`)
)

// loadDocs reads the pages in the docs directory of fsys, in path order so the index is the same on every start
func loadDocs(fsys fs.FS) ([]docSection, error) {
	var sections []docSection
	err := fs.WalkDir(fsys, "docs", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		ext := path.Ext(name)
		if d.IsDir() || (ext != ".md" && ext != ".mdx") {
			return nil
		}
		source, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		page, err := parsePage(strings.TrimSuffix(strings.TrimPrefix(name, "docs/"), ext), string(source))
		if err != nil {
			return fmt.Errorf("failed to read docs page %s: %w", name, err)
		}
		sections = append(sections, page...)
		return nil
	})
	return sections, err
}

// parsePage splits a page into its intro and a section per second and third level heading, with the site's components reduced to plain Markdown
func parsePage(slug, source string) ([]docSection, error) {
	// The frontmatter holds the page's title and the one-sentence description the website shows under it
	var meta struct {
		Title       string `yaml:"title"`
		Description string `yaml:"description"`
	}
	body := source
	if rest, ok := strings.CutPrefix(source, "---\n"); ok {
		front, after, found := strings.Cut(rest, "\n---\n")
		if !found {
			return nil, errors.New("the frontmatter never ends")
		}
		err := yaml.Unmarshal([]byte(front), &meta)
		if err != nil {
			return nil, fmt.Errorf("invalid frontmatter: %w", err)
		}
		body = after
	}
	if meta.Title == "" {
		return nil, errors.New("the page has no title")
	}
	pageURL := docsSite + slug + "/"

	var sections []docSection
	current := docSection{Page: meta.Title, Heading: meta.Title, URL: pageURL}
	var text strings.Builder
	if meta.Description != "" {
		text.WriteString("> " + meta.Description + "\n\n")
	}
	flush := func() {
		current.Body = cleanSection(text.String(), pageURL)
		if current.Body != "" {
			sections = append(sections, current)
		}
		text.Reset()
	}

	var section string
	inCode, inTag := false, false
	for line := range strings.SplitSeq(body, "\n") {
		trimmed := strings.TrimSpace(line)

		// A # inside a code block is a shell or YAML comment, never a heading, and code stays as it is
		if strings.HasPrefix(trimmed, "```") {
			inCode = !inCode
		}
		if inCode || strings.HasPrefix(trimmed, "```") {
			text.WriteString(line + "\n")
			continue
		}

		// A component tag that spans several lines, such as a screenshot with its alt text, is dropped up to its end
		if inTag {
			inTag = !strings.HasSuffix(trimmed, ">")
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "import "):
			continue
		case isComponentTag(trimmed):
			// A tab's label says what the content below it is for, while screenshots, diagrams, cards and wrappers carry nothing to read
			if m := tabLabel.FindStringSubmatch(trimmed); m != nil {
				text.WriteString("**" + m[1] + "**\n")
			}
			inTag = !strings.HasSuffix(trimmed, ">")
			continue
		case strings.HasPrefix(trimmed, ":::"):
			// An aside like :::caution[gVisor] keeps its kind and title as a label, and its closing ::: goes away
			if label := asideLabel(trimmed); label != "" {
				text.WriteString("**" + label + "**\n")
			}
			continue
		}

		// Each second and third level heading starts a section named after its page and the headings above it
		switch {
		case strings.HasPrefix(line, "## "):
			flush()
			section = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			current = docSection{Page: meta.Title, Heading: meta.Title + " › " + section, URL: pageURL + "#" + headingAnchor(section)}
		case strings.HasPrefix(line, "### "):
			flush()
			heading := strings.TrimSpace(strings.TrimPrefix(line, "### "))
			name := meta.Title + " › " + heading
			if section != "" {
				name = meta.Title + " › " + section + " › " + heading
			}
			current = docSection{Page: meta.Title, Heading: name, URL: pageURL + "#" + headingAnchor(heading)}
		default:
			text.WriteString(line + "\n")
		}
	}
	flush()
	return sections, nil
}

// isComponentTag reports whether a line opens or closes an Astro component, whose names start with a capital letter unlike HTML
func isComponentTag(line string) bool {
	name := strings.TrimPrefix(strings.TrimPrefix(line, "<"), "/")
	r, _ := utf8.DecodeRuneInString(name)
	return strings.HasPrefix(line, "<") && unicode.IsUpper(r)
}

// asideLabel turns an aside's opening line such as :::caution[gVisor] into a label such as "Caution: gVisor", and its closing line into nothing
func asideLabel(line string) string {
	kind, title, _ := strings.Cut(strings.TrimPrefix(line, ":::"), "[")
	if kind == "" {
		return ""
	}
	label := strings.ToUpper(kind[:1]) + kind[1:]
	if title = strings.TrimSuffix(title, "]"); title != "" {
		label += ": " + title
	}
	return label
}

// headingAnchor is the fragment the website gives a heading, lowercase with hyphens for spaces and without punctuation
func headingAnchor(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// cleanSection resolves links against the page's URL, since an agent can follow umpteenth.dev links but not relative ones
func cleanSection(body, pageURL string) string {
	base, _ := url.Parse(pageURL)
	text := markdownLink.ReplaceAllStringFunc(body, func(link string) string {
		m := markdownLink.FindStringSubmatch(link)
		target, err := url.Parse(m[2])
		if err != nil || target.IsAbs() {
			return link
		}
		return m[1] + "(" + base.ResolveReference(target).String() + ")"
	})
	return strings.TrimSpace(extraBlankLines.ReplaceAllString(text, "\n\n"))
}

// extraBlankLines are the blank lines left where component tags were removed
var extraBlankLines = regexp.MustCompile(`\n{3,}`)

// docsIndex searches the docs with SQLite's full-text search and BM25 ranking
type docsIndex struct {
	db *sql.DB
	// empty is set for a build without the docs pages
	empty bool
}

// newDocsIndex loads the sections into an in-memory full-text index, which takes milliseconds for the whole documentation
func newDocsIndex(ctx context.Context, sections []docSection) (*docsIndex, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("failed to open the docs index: %w", err)
	}

	// Every connection to :memory: is a database of its own, so the index lives on one connection that is never closed
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)

	// The Porter stemmer lets "schedules" find "schedule", and unicode61 folds case and accents
	// The URL is stored without being searched, since it only repeats the page and heading
	_, err = db.ExecContext(ctx, `CREATE VIRTUAL TABLE docs USING fts5(page, heading, url UNINDEXED, body, tokenize = 'porter unicode61')`)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to create the docs index: %w", err)
	}
	for _, s := range sections {
		_, err = db.ExecContext(ctx, `INSERT INTO docs (page, heading, url, body) VALUES (?, ?, ?, ?)`, s.Page, s.Heading, s.URL, s.Body)
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed to index the docs: %w", err)
		}
	}
	return &docsIndex{db: db, empty: len(sections) == 0}, nil
}

// search returns the sections that match any of the query's words, those matching more and rarer words first
func (i *docsIndex) search(ctx context.Context, query string, limit int) ([]docSection, error) {
	// Each word is quoted, so nothing the agent types is read as FTS5 syntax
	var terms []string
	seen := map[string]bool{}
	for _, word := range queryWord.FindAllString(strings.ToLower(query), 20) {
		if !seen[word] {
			seen[word] = true
			terms = append(terms, `"`+word+`"`)
		}
	}
	if len(terms) == 0 {
		return nil, argumentError("The query needs at least one word")
	}

	// A word in a page title or heading counts more than one in the text
	rows, err := i.db.QueryContext(ctx, `SELECT page, heading, url, body FROM docs WHERE docs MATCH ? ORDER BY bm25(docs, 2.0, 4.0, 0.0, 1.0) LIMIT ?`, strings.Join(terms, " OR "), limit)
	if err != nil {
		return nil, fmt.Errorf("failed to search the docs: %w", err)
	}
	defer rows.Close()
	var sections []docSection
	for rows.Next() {
		var s docSection
		err = rows.Scan(&s.Page, &s.Heading, &s.URL, &s.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read a docs section: %w", err)
		}
		sections = append(sections, s)
	}
	return sections, rows.Err()
}

// searchDocsTool is the one tool that isn't a REST operation, since the docs belong to the release rather than to a workspace
var searchDocsTool = &mcp.Tool{
	Name:  "search_docs",
	Title: "Search docs",
	Description: "Searches the Umpteenth documentation of the release this server runs and returns the best matching sections in full. " +
		"Use it before writing a job instruction, and whenever a field, setting, error or behavior is unclear. " +
		"Query with a few keywords, such as \"cron timezone\" or \"secret environment variable\".",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "minLength": 1, "maxLength": maxDocsQuery, "description": "Keywords to look for"},
			"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": maxDocsResults, "default": defaultDocsResults, "description": "How many sections to return"},
		},
		"required":             []string{"query"},
		"additionalProperties": false,
	},
	Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(false)},
}

// handler answers a search with the matching sections as Markdown
func (i *docsIndex) handler(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// The SDK doesn't check arguments against the schema of a tool added without types, so they are checked here
	var args struct {
		Query string `json:"query"`
		Limit *int   `json:"limit"`
	}
	dec := json.NewDecoder(bytes.NewReader(req.Params.Arguments))
	dec.DisallowUnknownFields()
	// A bad argument is the agent's to fix, so it gets a tool result it can read instead of a protocol error
	if valid := len(req.Params.Arguments) == 0 || dec.Decode(&args) == nil; !valid {
		return errorResult("The arguments must be an object with a query and an optional limit"), nil
	}
	limit := defaultDocsResults
	if args.Limit != nil {
		limit = *args.Limit
	}
	switch {
	case strings.TrimSpace(args.Query) == "":
		return errorResult(`Missing required argument "query"`), nil
	case len(args.Query) > maxDocsQuery:
		return errorResult(fmt.Sprintf("The query can have at most %d characters", maxDocsQuery)), nil
	case limit < 1 || limit > maxDocsResults:
		return errorResult(fmt.Sprintf("The limit must be between 1 and %d", maxDocsResults)), nil
	}

	// A build without the pages, such as a local one, can still tell the agent where the docs are
	if i.empty {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "This build of Umpteenth doesn't include its docs, read them at " + docsSite + " instead."}}}, nil
	}

	sections, err := i.search(ctx, args.Query, limit)
	if argErr, ok := errors.AsType[argumentError](err); ok {
		return errorResult(argErr.Error()), nil
	} else if err != nil {
		return nil, err
	}
	if len(sections) == 0 {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "No section of the docs matches. Try other or fewer words."}}}, nil
	}

	// Each section comes with the headings it sits under and its URL, so the agent can tell the user where to read on
	var text strings.Builder
	for n, s := range sections {
		if n > 0 {
			text.WriteString("\n\n---\n\n")
		}
		fmt.Fprintf(&text, "## %s\n%s\n\n%s", s.Heading, s.URL, truncateSection(s.Body))
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text.String()}}}, nil
}

// truncateSection cuts a section at maxSectionChars, on a character boundary
func truncateSection(body string) string {
	if len(body) <= maxSectionChars {
		return body
	}
	cut := maxSectionChars
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return body[:cut] + "\n\n[The section continues, search with more specific words to find the rest]"
}

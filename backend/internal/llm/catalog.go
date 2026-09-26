package llm

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
)

//go:generate go run ./gencatalog catalog.json

// CatalogEntry is a known model with its capabilities and list price
type CatalogEntry struct {
	Kind  string `json:"kind"`
	Model string `json:"model"`
	Label string `json:"label"`
	// Caps.Context is the context window in tokens
	Caps  Caps  `json:"caps"`
	Price Price `json:"price"`
}

// ModelCatalog is the model catalog built from models.dev
type ModelCatalog struct {
	// FetchedAt is when models.dev was read, in Unix seconds
	FetchedAt int64 `json:"fetchedAt"`
	// Models are the Anthropic and OpenAI models agents can run on
	Models []CatalogEntry `json:"models"`
	// Families holds the traits of model families across every host, keyed by FamilyKey, for the models OpenAI-compatible servers describe poorly
	Families map[string]Caps `json:"families"`
}

// bundledCatalog is the models.dev snapshot taken at build time with go generate, used until a refresh replaces it
//
//go:embed catalog.json
var bundledCatalog []byte

var current atomic.Pointer[ModelCatalog]

// An unreadable snapshot leaves the catalog empty instead of failing, so go generate can rebuild it with the generator that imports this package
// TestBundledCatalogIsValid keeps a broken snapshot from shipping
func init() {
	c, err := DecodeCatalog(bundledCatalog)
	if err != nil {
		c = &ModelCatalog{Models: []CatalogEntry{}, Families: map[string]Caps{}}
	}
	current.Store(c)
}

// DecodeCatalog reads a catalog written by EncodeCatalog
func DecodeCatalog(data []byte) (*ModelCatalog, error) {
	var c ModelCatalog
	err := json.Unmarshal(data, &c)
	if err != nil {
		return nil, err
	}
	if len(c.Models) == 0 {
		return nil, fmt.Errorf("the catalog lists no models")
	}
	if c.Families == nil {
		c.Families = map[string]Caps{}
	}
	return &c, nil
}

// EncodeCatalog serializes a catalog for storage
func EncodeCatalog(c *ModelCatalog) ([]byte, error) {
	return json.Marshal(c)
}

// CurrentCatalog returns the catalog in use, which callers must not modify
func CurrentCatalog() *ModelCatalog {
	return current.Load()
}

// SetCatalog replaces the catalog in use unless the current one is newer, and reports whether it did
// An older refresh stored in the database must not replace the snapshot of a newer build
func SetCatalog(c *ModelCatalog) bool {
	for {
		old := current.Load()
		if old.FetchedAt > c.FetchedAt {
			return false
		}
		if current.CompareAndSwap(old, c) {
			return true
		}
	}
}

// CatalogFor returns the catalog models of one provider kind
func CatalogFor(kind string) []CatalogEntry {
	var out []CatalogEntry
	for _, e := range current.Load().Models {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// dateSuffix matches the snapshot suffix of pinned model IDs, such as -20251001 or -2025-08-07
var dateSuffix = regexp.MustCompile(`-\d{4}-?\d{2}-?\d{2}$`)

// LookupModel finds a catalog entry by provider kind and model ID
// Pinned snapshot IDs such as claude-haiku-4-5-20251001 resolve to their alias
func LookupModel(kind, model string) (CatalogEntry, bool) {
	model = strings.TrimSpace(model)
	models := current.Load().Models
	for _, candidate := range []string{model, dateSuffix.ReplaceAllString(model, "")} {
		for _, e := range models {
			if e.Kind == kind && e.Model == candidate {
				return e, true
			}
		}
	}
	return CatalogEntry{}, false
}

// LookupFamily returns what models.dev knows about the family of an open model, whatever host-specific name it is served under
func LookupFamily(model string) (Caps, bool) {
	key := FamilyKey(model)
	if key == "" {
		return Caps{}, false
	}
	caps, ok := current.Load().Families[key]
	return caps, ok
}

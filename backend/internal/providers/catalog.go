package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/providers/providersdb"
)

const (
	// catalogSource keys the stored catalog, leaving room for other sources later
	catalogSource = "models.dev"
	// catalogCheckInterval bounds how often a replica asks the database whether another replica stored a newer catalog
	catalogCheckInterval = time.Minute
	// maxCatalogSize caps the models.dev download, which is a few megabytes today
	maxCatalogSize = 64 << 20
)

// catalogStore keeps the latest models.dev catalog in the database, so every replica uses what the last refresh fetched and a restart keeps it
type catalogStore struct {
	queries *providersdb.Queries
	client  *http.Client
	url     string

	mu        sync.Mutex
	checkedAt time.Time
}

func newCatalogStore(queries *providersdb.Queries) *catalogStore {
	return &catalogStore{queries: queries, client: &http.Client{Timeout: time.Minute}, url: llm.ModelsDevURL}
}

// load installs the stored catalog when it is newer than the one in use
func (c *catalogStore) load(ctx context.Context) error {
	row, err := c.queries.GetModelCatalog(ctx, catalogSource)
	if database.IsNotFound(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to load the model catalog: %w", err)
	}
	if row.FetchedAt <= llm.CurrentCatalog().FetchedAt {
		return nil
	}
	catalog, err := llm.DecodeCatalog([]byte(row.Data))
	if err != nil {
		return fmt.Errorf("the stored model catalog is invalid: %w", err)
	}
	llm.SetCatalog(catalog)
	return nil
}

// ensureFresh picks up a catalog another replica refreshed, checking the database at most once a minute
func (c *catalogStore) ensureFresh(ctx context.Context) {
	c.mu.Lock()
	if time.Since(c.checkedAt) < catalogCheckInterval {
		c.mu.Unlock()
		return
	}
	c.checkedAt = time.Now()
	c.mu.Unlock()

	fetchedAt, err := c.queries.GetModelCatalogFetchedAt(ctx, catalogSource)
	if err != nil || fetchedAt <= llm.CurrentCatalog().FetchedAt {
		return
	}
	err = c.load(ctx)
	if err != nil {
		slog.WarnContext(ctx, "Failed to load the refreshed model catalog", slog.Any("error", err))
	}
}

// refresh downloads models.dev and stores the catalog built from it
func (c *catalogStore) refresh(ctx context.Context) error {
	data, err := c.download(ctx)
	if err != nil {
		return err
	}
	catalog, err := llm.ParseModelsDev(data, time.Now())
	if err != nil {
		return err
	}
	encoded, err := llm.EncodeCatalog(catalog)
	if err != nil {
		return fmt.Errorf("failed to encode the model catalog: %w", err)
	}

	// The database copy is what other replicas and the next start read
	err = c.queries.SaveModelCatalog(ctx, providersdb.SaveModelCatalogParams{Source: catalogSource, Data: string(encoded), FetchedAt: catalog.FetchedAt})
	if err != nil {
		return fmt.Errorf("failed to store the model catalog: %w", err)
	}
	llm.SetCatalog(catalog)
	return nil
}

func (c *catalogStore) download(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download the model catalog: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download the model catalog: %s answered %s", c.url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogSize+1))
	if err != nil {
		return nil, fmt.Errorf("failed to download the model catalog: %w", err)
	}
	if len(data) > maxCatalogSize {
		return nil, errors.New("the model catalog is larger than 64 MB")
	}
	return data, nil
}

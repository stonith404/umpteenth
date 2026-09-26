package httpserver

import "github.com/stonith404/umpteenth/backend/internal/listquery"

// ListParams are the shared query parameters of every list endpoint, embedded in each list input struct
type ListParams struct {
	Page     int    `query:"page" minimum:"1" default:"1" doc:"1-based page number"`
	PageSize int    `query:"pageSize" minimum:"1" maximum:"100" default:"25" doc:"Items per page"`
	Sort     string `query:"sort" maxLength:"200" doc:"Comma-separated sort keys, prefix with - for descending"`
	Search   string `query:"search" maxLength:"200" doc:"Free-text search"`
}

// ToQuery converts the HTTP parameters into listquery parameters
func (p ListParams) ToQuery() listquery.Params {
	return listquery.Params{Page: p.Page, PageSize: p.PageSize, Sort: p.Sort, Search: p.Search}
}

// Paginated is the envelope of every list response
type Paginated[T any] struct {
	Items    []T   `json:"items" nullable:"false"`
	Page     int   `json:"page"`
	PageSize int   `json:"pageSize"`
	Total    int64 `json:"total"`
}

// PaginatedOutput wraps a paginated body for Huma
type PaginatedOutput[T any] struct {
	Body Paginated[T]
}

// NewPaginated builds a list response, never returning a nil items slice
func NewPaginated[T any](items []T, p ListParams, total int64) *PaginatedOutput[T] {
	if items == nil {
		items = []T{}
	}
	return &PaginatedOutput[T]{Body: Paginated[T]{Items: items, Page: p.Page, PageSize: p.PageSize, Total: total}}
}

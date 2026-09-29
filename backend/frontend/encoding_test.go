//go:build unit

package frontend

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAcceptedCodings(t *testing.T) {
	tests := []struct {
		name   string
		header []string
		want   []string
	}{
		{name: "browser", header: []string{"gzip, deflate, br, zstd"}, want: []string{"br", "gzip"}},
		{name: "none", header: nil, want: nil},
		{name: "gzip only", header: []string{"gzip, deflate"}, want: []string{"gzip"}},
		{name: "brotli refused", header: []string{"br;q=0, gzip"}, want: []string{"gzip"}},
		{name: "gzip preferred", header: []string{"br;q=0.5, gzip;q=0.8"}, want: []string{"gzip", "br"}},
		{name: "spaces and case", header: []string{"GZIP ; Q=1 , BR ; q=0.1"}, want: []string{"gzip", "br"}},
		{name: "substring is no match", header: []string{"xbr, gzipped"}, want: nil},
		{name: "wildcard", header: []string{"*"}, want: []string{"br", "gzip"}},
		{name: "wildcard without brotli", header: []string{"*, br;q=0"}, want: []string{"gzip"}},
		{name: "malformed quality", header: []string{"br;q=high, gzip;q=2, deflate"}, want: nil},
		{name: "several header lines", header: []string{"deflate", "br"}, want: []string{"br"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, c := range acceptedCodings(tt.header) {
				got = append(got, c.name)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

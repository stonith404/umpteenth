//go:build !exclude_frontend

package frontend

import (
	"embed"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler serves the SPA: static assets with precompressed variants, and index.html for every other path
func Handler() (http.Handler, error) {
	root, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}
	index, err := fs.ReadFile(root, "index.html")
	if err != nil {
		return nil, ErrFrontendNotIncluded
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")

		// Unknown API paths must not fall back to the SPA shell
		if strings.HasPrefix(p, "api/") || p == "api" || strings.HasPrefix(p, "hooks/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"code":"not_found","message":"Not found"}`)
			return
		}

		if p != "" && p != "index.html" {
			if serveAsset(w, r, root, p) {
				return
			}
		}

		// Everything else is a client-side route
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(index)
	}), nil
}

func serveAsset(w http.ResponseWriter, r *http.Request, root fs.FS, p string) bool {
	info, err := fs.Stat(root, p)
	if err != nil || info.IsDir() {
		return false
	}

	// Hashed build output never changes, everything else is revalidated
	if strings.HasPrefix(p, "_app/immutable/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=300")
	}
	if ct := mime.TypeByExtension(path.Ext(p)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Add("Vary", "Accept-Encoding")

	// Prefer the precompressed sidecars SvelteKit generates
	accept := r.Header.Get("Accept-Encoding")
	for _, enc := range []struct{ name, ext string }{{"br", ".br"}, {"gzip", ".gz"}} {
		if !strings.Contains(accept, enc.name) {
			continue
		}
		if data, err := fs.ReadFile(root, p+enc.ext); err == nil {
			w.Header().Set("Content-Encoding", enc.name)
			_, _ = w.Write(data)
			return true
		}
	}

	data, err := fs.ReadFile(root, p)
	if err != nil {
		return false
	}
	_, _ = w.Write(data)
	return true
}

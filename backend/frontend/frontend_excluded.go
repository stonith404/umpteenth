//go:build exclude_frontend

package frontend

import "net/http"

// Handler reports that the frontend is not part of this build
func Handler() (http.Handler, error) {
	return nil, ErrFrontendNotIncluded
}

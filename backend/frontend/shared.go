// Package frontend serves the embedded SvelteKit SPA
package frontend

import "errors"

// ErrFrontendNotIncluded is returned when the binary was built with the exclude_frontend tag
var ErrFrontendNotIncluded = errors.New("frontend is not included in this build")

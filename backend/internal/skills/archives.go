//go:build !e2etest

package skills

import "context"

// archives is where repository tarballs come from
func (m *Module) archives(_ context.Context) string {
	return m.githubArchives
}

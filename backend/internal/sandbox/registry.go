package sandbox

import (
	"fmt"
	"strings"

	"github.com/distribution/reference"
)

// InRegistry matches a repository boundary so credentials never authorize sibling repositories on the same host
func InRegistry(ref, repository string) bool {
	repository = strings.TrimRight(strings.TrimPrefix(strings.TrimPrefix(repository, "https://"), "http://"), "/")
	if repository == "" {
		return false
	}
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return false
	}
	// Registry settings may name a Docker Hub namespace without an explicit host
	for _, image := range []string{named.Name(), reference.FamiliarName(named)} {
		if image == repository || strings.HasPrefix(image, repository+"/") {
			return true
		}
	}
	return false
}

// CheckRegistrySource reserves the configured registry for the originating job's images
// Every other repository on that host is refused because node and build-client credentials are host-scoped
func CheckRegistrySource(ref, repository, jobID, defaultImage string) error {
	if repository == "" || ref == defaultImage || (defaultImage != "" && strings.HasPrefix(ref, defaultImage+"@")) {
		return nil
	}
	configured, err := reference.ParseNormalizedNamed(strings.TrimRight(strings.TrimPrefix(strings.TrimPrefix(repository, "https://"), "http://"), "/") + "/umpteenth-registry-scope")
	if err != nil {
		return err
	}
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return fmt.Errorf("invalid image reference %q: %w", ref, err)
	}
	if reference.Domain(named) != reference.Domain(configured) {
		return nil
	}
	if !InRegistry(ref, repository) {
		return fmt.Errorf("image %s is outside sandbox.registry.repository", ref)
	}
	if jobID == "" || !InRegistry(ref, strings.TrimRight(repository, "/")+"/job-"+jobID) {
		return fmt.Errorf("image %s belongs to another job", ref)
	}
	return nil
}

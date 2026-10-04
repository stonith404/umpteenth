package kubernetes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// registryHost is the host of the configured job image registry
func (a *Adapter) registryHost() string {
	if a.cfg.Registry == "" {
		return ""
	}
	repo, err := name.NewRepository(a.cfg.Registry)
	if err != nil {
		return ""
	}
	return repo.RegistryStr()
}

// parseRef parses an image reference, treating the configured registry as insecure when it is
func (a *Adapter) parseRef(ref string) (name.Reference, error) {
	parsed, err := name.ParseReference(ref)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference %q: %w", ref, err)
	}
	if a.cfg.InsecureRegistry && parsed.Context().RegistryStr() == a.registryHost() {
		return name.ParseReference(ref, name.Insecure)
	}
	return parsed, nil
}

// inRegistry reports whether a reference points into the configured registry
func (a *Adapter) inRegistry(ref name.Reference) bool {
	return sandbox.InRegistry(ref.Name(), a.cfg.Registry)
}

// remoteOptions authenticate against the configured registry and go anonymously everywhere else
// Every other registry is reached through the registry transport, since a Dockerfile names it and could point it, or a redirect or token realm it returns, at a private network
func (a *Adapter) remoteOptions(ctx context.Context, ref name.Reference) []remote.Option {
	auth := authn.Anonymous
	if a.inRegistry(ref) && a.cfg.RegistryUsername != "" {
		auth = authn.FromConfig(authn.AuthConfig{Username: a.cfg.RegistryUsername, Password: a.cfg.RegistryPassword})
	}
	opts := []remote.Option{remote.WithContext(ctx), remote.WithAuth(auth), remote.WithUserAgent("umpteenth")}
	if !a.inRegistry(ref) && a.cfg.RegistryTransport != nil {
		opts = append(opts, remote.WithTransport(a.cfg.RegistryTransport))
	}
	return opts
}

// head resolves a reference in its registry, reporting a missing image as false
func (a *Adapter) head(ctx context.Context, ref name.Reference) (*v1.Descriptor, bool, error) {
	desc, err := remote.Head(ref, a.remoteOptions(ctx, ref)...)
	if isMissing(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("failed to look up %s: %w", ref, err)
	}
	return desc, true, nil
}

// isMissing reports whether a registry said the image or repository doesn't exist
func isMissing(err error) bool {
	terr, ok := errors.AsType[*transport.Error](err)
	if !ok {
		return false
	}
	if terr.StatusCode == http.StatusNotFound {
		return true
	}
	for _, d := range terr.Errors {
		if d.Code == transport.ManifestUnknownErrorCode || d.Code == transport.NameUnknownErrorCode {
			return true
		}
	}
	return false
}

// isUnsupported reports whether a registry refused a delete it doesn't offer, like many hosted registries do
func isUnsupported(err error) bool {
	terr, ok := errors.AsType[*transport.Error](err)
	if !ok {
		return false
	}
	if terr.StatusCode == http.StatusMethodNotAllowed {
		return true
	}
	for _, d := range terr.Errors {
		if d.Code == transport.UnsupportedErrorCode {
			return true
		}
	}
	return false
}

// ensurePullSecret stores the registry credentials as the Secret nodes pull job images with, and build pods push with
func (a *Adapter) ensurePullSecret(ctx context.Context) error {
	if a.pullSecret == "" {
		return nil
	}

	// Docker Hub is keyed by its legacy URL in Docker config files, every other registry by its host
	host := a.registryHost()
	if host == name.DefaultRegistry {
		host = "https://index.docker.io/v1/"
	}
	auth := base64.StdEncoding.EncodeToString([]byte(a.cfg.RegistryUsername + ":" + a.cfg.RegistryPassword))
	config, err := json.Marshal(map[string]any{"auths": map[string]any{host: map[string]string{
		"username": a.cfg.RegistryUsername,
		"password": a.cfg.RegistryPassword,
		"auth":     auth,
	}}})
	if err != nil {
		return err
	}

	// Create the Secret, or update it when the credentials changed since it was written
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: a.pullSecret, Namespace: a.ns, Labels: map[string]string{labelInstance: labelValue(a.cfg.InstanceID), labelRole: roleRegistryAuth}},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: config},
	}
	existing := &corev1.Secret{}
	err = a.kube.get(ctx, "secrets", a.pullSecret, existing)
	switch {
	case apierrors.IsNotFound(err):
		err = a.kube.create(ctx, "secrets", secret, &corev1.Secret{})
	case err == nil && string(existing.Data[corev1.DockerConfigJsonKey]) != string(config):
		existing.Data = secret.Data
		err = a.kube.update(ctx, "secrets", a.pullSecret, existing, &corev1.Secret{})
	}
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to store the registry credentials: %w", err)
	}
	return nil
}

package docker

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/docker/docker/client"
)

// withPodmanBuilds makes a client send image builds in the shape Podman's Docker-compatible build endpoint reads
// It goes after client.FromEnv, whose transport it wraps, and only on a client that does nothing but build, since hijacked connections need the plain transport
func withPodmanBuilds(c *client.Client) error {
	hc := c.HTTPClient()
	hc.Transport = podmanBuildTransport{base: hc.Transport}
	return client.WithHTTPClient(hc)(c)
}

// podmanBuildTransport rewrites the build options Podman reads differently from Docker
// Podman ignores a network name in networkmode and builds in the host's network namespace unless nsoptions says otherwise, and it expects extrahosts as a JSON list
type podmanBuildTransport struct {
	base http.RoundTripper
}

// podmanNamespaceOption mirrors Buildah's NamespaceOption, the element of Podman's nsoptions list
type podmanNamespaceOption struct {
	Name string
	Host bool   `json:",omitempty"`
	Path string `json:",omitempty"`
}

func (t podmanBuildTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/build") {
		return t.base.RoundTrip(req)
	}
	query := req.URL.Query()

	// The network becomes a namespace option, since Podman reads networkmode only as a policy
	if mode := query.Get("networkmode"); mode != "" {
		ns := podmanNamespaceOption{Name: "network"}
		policy := "NetworkEnabled"
		switch mode {
		case "none":
			policy = "NetworkDisabled"
		case "host":
			ns.Host = true
		case "default", "bridge":
			// Podman's own default is the host's namespace, so the private one Docker would give is spelled out
		default:
			ns.Path = mode
		}
		options, err := json.Marshal([]podmanNamespaceOption{ns})
		if err != nil {
			return nil, err
		}
		query.Set("networkmode", policy)
		query.Set("nsoptions", string(options))
	}

	// Docker's client repeats extrahosts, while Podman decodes a single JSON list
	if hosts, ok := query["extrahosts"]; ok {
		list, err := json.Marshal(hosts)
		if err != nil {
			return nil, err
		}
		query.Set("extrahosts", string(list))
	}

	// A RoundTripper must not change the request it was given
	req = req.Clone(req.Context())
	req.URL.RawQuery = query.Encode()
	return t.base.RoundTrip(req)
}

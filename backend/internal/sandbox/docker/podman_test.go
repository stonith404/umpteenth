//go:build unit

package docker

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingTransport keeps the last request it was asked to send
type recordingTransport struct{ req *http.Request }

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.req = req
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
}

// sendBuild sends a build request with the given query through the Podman transport and returns the query that reached the engine
func sendBuild(t *testing.T, method, path string, query url.Values) url.Values {
	t.Helper()
	base := &recordingTransport{}
	req, err := http.NewRequest(method, "http://docker"+path+"?"+query.Encode(), http.NoBody)
	require.NoError(t, err)
	res, err := podmanBuildTransport{base: base}.RoundTrip(req)
	require.NoError(t, err)
	_ = res.Body.Close()
	return base.req.URL.Query()
}

func TestPodmanBuildsJoinTheNamedNetwork(t *testing.T) {
	// A network name becomes a namespace path, since Podman would otherwise build in the host's network namespace
	sent := sendBuild(t, http.MethodPost, "/v1.41/build", url.Values{"networkmode": {"ump-run-build-1"}, "t": {"job:1"}})
	assert.Equal(t, "NetworkEnabled", sent.Get("networkmode"))
	assert.JSONEq(t, `[{"Name":"network","Path":"ump-run-build-1"}]`, sent.Get("nsoptions"))
	assert.Equal(t, "job:1", sent.Get("t"))

	// No network disables it outright
	sent = sendBuild(t, http.MethodPost, "/v1.41/build", url.Values{"networkmode": {"none"}})
	assert.Equal(t, "NetworkDisabled", sent.Get("networkmode"))
	assert.JSONEq(t, `[{"Name":"network"}]`, sent.Get("nsoptions"))
}

func TestPodmanBuildsGetExtraHostsAsAList(t *testing.T) {
	sent := sendBuild(t, http.MethodPost, "/v1.41/build", url.Values{"extrahosts": {"umpteenth:10.89.0.2", "other:10.89.0.3"}})
	assert.JSONEq(t, `["umpteenth:10.89.0.2","other:10.89.0.3"]`, sent.Get("extrahosts"))
}

func TestPodmanTransportLeavesOtherRequestsAlone(t *testing.T) {
	sent := sendBuild(t, http.MethodPost, "/v1.41/containers/create", url.Values{"networkmode": {"none"}})
	assert.Equal(t, "none", sent.Get("networkmode"))
	assert.Empty(t, sent.Get("nsoptions"))
}

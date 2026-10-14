//go:build unit

package sandbox

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testCertificate is a self-signed CA certificate in PEM
func testCertificate(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Proxy CA"}, NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestLoadCACertificates(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, content []byte) string {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, content, 0o600))
		return path
	}

	// No file means no extra certificates
	certs, err := LoadCACertificates("")
	require.NoError(t, err)
	require.Nil(t, certs)

	// A bundle loads, and a missing final newline is added so it can be appended to another
	cert := testCertificate(t)
	certs, err = LoadCACertificates(write("ok.crt", append(cert, cert[:len(cert)-1]...)))
	require.NoError(t, err)
	require.Equal(t, byte('\n'), certs[len(certs)-1])

	// Files that hold no certificate, or a private key, are refused at startup
	_, err = LoadCACertificates(write("empty.crt", []byte("not a certificate")))
	require.ErrorContains(t, err, "no PEM certificates")
	_, err = LoadCACertificates(write("key.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("secret")})))
	require.ErrorContains(t, err, "PRIVATE KEY")
	_, err = LoadCACertificates(filepath.Join(dir, "missing.crt"))
	require.Error(t, err)
}

func TestBrokerEnvPointsToolsAtTheCACertificates(t *testing.T) {
	spec := Spec{RunID: "run-1", Network: NetworkNone, Env: map[string]string{"SSL_CERT_FILE": "/own.pem"}}
	require.NotContains(t, BrokerEnv(spec, "broker:8081", nil), "NODE_EXTRA_CA_CERTS="+CACertificatesPath)

	// The job's own setting wins over the bundle
	spec.CACertificates = true
	env := BrokerEnv(spec, "broker:8081", nil)
	require.Contains(t, env, "NODE_EXTRA_CA_CERTS="+CACertificatesPath)
	require.Contains(t, env, "REQUESTS_CA_BUNDLE="+CABundlePath)
	require.Contains(t, env, "SSL_CERT_FILE=/own.pem")
	count := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, "SSL_CERT_FILE=") {
			count++
		}
	}
	require.Equal(t, 1, count)
}

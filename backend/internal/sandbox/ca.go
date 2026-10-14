package sandbox

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Where a sandbox gets the extra CA certificates of sandbox.ca_certificates
const (
	// CACertificatesPath holds only the extra certificates, where update-ca-certificates finds them again when a job reinstalls the system bundle
	CACertificatesPath = "/usr/local/share/ca-certificates/umpteenth.crt"
	// CABundlePath holds the image's own trusted certificates followed by the extra ones, for tools that read a single bundle instead of the system one
	CABundlePath = "/etc/ssl/umpteenth/ca-bundle.crt"
)

// CAEnv points tools that ignore the system trust store at the extra certificates
// Node only adds NODE_EXTRA_CA_CERTS to its own roots, while the others replace theirs and so get the whole bundle
var CAEnv = map[string]string{
	"SSL_CERT_FILE":       CABundlePath,
	"REQUESTS_CA_BUNDLE":  CABundlePath,
	"NODE_EXTRA_CA_CERTS": CACertificatesPath,
}

// LoadCACertificates reads a PEM file of CA certificates and rejects one that holds none, since a typo would otherwise only show as TLS failures inside runs
func LoadCACertificates(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path) // #nosec G304 -- the operator names the file in sandbox.ca_certificates
	if err != nil {
		return nil, fmt.Errorf("failed to read sandbox.ca_certificates: %w", err)
	}

	// Every PEM block must be a certificate, and there must be at least one
	count := 0
	for rest := data; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("sandbox.ca_certificates holds a %s block, but only certificates belong there", block.Type)
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return nil, fmt.Errorf("sandbox.ca_certificates holds an invalid certificate: %w", err)
		}
		count++
	}
	if count == 0 {
		return nil, errors.New("sandbox.ca_certificates holds no PEM certificates")
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	return data, nil
}

// TrustCACertificates installs extra CA certificates into a sandbox's trust store, before anything else runs in it
// ump trust appends them to the image's system bundles and writes CABundlePath, which CAEnv points tools at
func TrustCACertificates(ctx context.Context, sb Sandbox, certificates []byte) error {
	err := sb.PutFiles(ctx, []File{{Path: CACertificatesPath, Mode: 0o644, Owner: UserRoot, Content: certificates}})
	if err != nil {
		return fmt.Errorf("failed to copy the CA certificates: %w", err)
	}

	// Nothing but the adapter has run in the sandbox yet, so the ump it installed can still be trusted to run as root
	var out bytes.Buffer
	res, err := sb.Exec(ctx, ExecRequest{
		Cmd:     []string{UmpBinary, "trust", CACertificatesPath, CABundlePath},
		User:    UserRoot,
		Stdout:  &out,
		Stderr:  &out,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("failed to install the CA certificates: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("failed to install the CA certificates: %s", strings.TrimSpace(out.String()))
	}
	return nil
}

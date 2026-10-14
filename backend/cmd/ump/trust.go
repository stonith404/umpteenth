package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// systemBundles are the trust stores of common distributions, which are often symlinks to one another
var systemBundles = []string{
	// Debian, Ubuntu, Alpine and Arch
	"/etc/ssl/certs/ca-certificates.crt",
	// Fedora and RHEL
	"/etc/pki/tls/certs/ca-bundle.crt",
	"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
	// openSUSE
	"/etc/ssl/ca-bundle.pem",
	// Alpine and others that keep OpenSSL's default name
	"/etc/ssl/cert.pem",
}

func init() {
	register("trust", command{
		Summary:  "Add PEM CA certificates to the system trust store and write a bundle with both",
		Internal: true,
		Run:      runTrust,
	})
}

// runTrust installs the extra CA certificates of sandbox.ca_certificates, so tools that read the system trust store accept them
func runTrust(args []string) int {
	if len(args) != 2 {
		_, _ = fmt.Fprintln(os.Stderr, "Usage: ump trust <certificates> <bundle>")
		return 2
	}
	if err := trust(args[0], args[1]); err != nil {
		errorf("trust", "%v", err)
		return 1
	}
	return 0
}

// trust appends the certificates to every system bundle of the image and writes bundle with the first one's certificates followed by them
func trust(certificatesPath, bundlePath string) error {
	extra, err := os.ReadFile(filepath.Join(layoutRoot, certificatesPath)) // #nosec G304 G703 -- the adapter names the file it just wrote
	if err != nil {
		return err
	}
	if !bytes.HasSuffix(extra, []byte("\n")) {
		extra = append(extra, '\n')
	}

	// Each real file is extended once, however many of the well-known names point at it
	var system []byte
	seen := map[string]bool{}
	for _, name := range systemBundles {
		path, err := filepath.EvalSymlinks(filepath.Join(layoutRoot, name))
		if err != nil || seen[path] {
			continue
		}
		seen[path] = true
		current, err := os.ReadFile(path) // #nosec G304 -- a well-known trust store
		if err != nil {
			continue
		}
		if system == nil {
			system = current
		}
		if bytes.Contains(current, extra) {
			continue
		}
		if err := appendCertificates(path, current, extra); err != nil {
			return fmt.Errorf("failed to extend %s: %w", name, err)
		}
	}

	// An image without a trust store still gets a bundle, which then only holds the extra certificates
	bundle := bytes.Clone(system)
	if len(bundle) > 0 && !bytes.HasSuffix(bundle, []byte("\n")) {
		bundle = append(bundle, '\n')
	}
	if !bytes.Contains(bundle, extra) {
		bundle = append(bundle, extra...)
	}
	path := filepath.Join(layoutRoot, bundlePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { // #nosec G301 G703 -- every user's tools read the bundle
		return err
	}
	return os.WriteFile(path, bundle, 0o644) // #nosec G306 G703 -- every user's tools read the bundle
}

// appendCertificates adds the certificates to the end of a bundle, on a line of their own
func appendCertificates(path string, current, extra []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0) // #nosec G304 -- a well-known trust store
	if err != nil {
		return err
	}
	if len(current) > 0 && !bytes.HasSuffix(current, []byte("\n")) {
		extra = append([]byte("\n"), extra...)
	}
	_, writeErr := f.Write(extra)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

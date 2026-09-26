// Package crypto holds the key derivation and symmetric encryption helpers built on the app.encryption_key option
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// KeyIDV1 identifies secrets encrypted with the instance key derived for secrets
// Storing it next to every ciphertext allows key rotation and per-workspace keys later
const KeyIDV1 = "v1"

// DeriveKey derives a purpose-specific 32-byte key from the master key
// Changing the purpose string or the derivation is a breaking change for existing data
func DeriveKey(master []byte, purpose string) ([]byte, error) {
	return hkdf.Key(sha256.New, master, nil, "umpteenth/"+purpose, 32)
}

// Encrypt seals plaintext with AES-256-GCM under a random nonce, which the ciphertext starts with
func Encrypt(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	return gcm.Seal(nil, nil, plaintext, nil), nil
}

// Decrypt opens a ciphertext produced by Encrypt
func Decrypt(key, ciphertext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, nil, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt: %w", err)
	}
	return plaintext, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}
	return cipher.NewGCMWithRandomNonce(block)
}

// Sign returns an HMAC-SHA256 signature of data, base64url-encoded
func Sign(key, data []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Verify checks a signature produced by Sign in constant time
func Verify(key, data []byte, signature string) bool {
	expected := Sign(key, data)
	return hmac.Equal([]byte(expected), []byte(signature))
}

// RandomToken returns a URL-safe random token with the given number of random bytes
func RandomToken(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

// HashToken returns the hex SHA-256 of a token, which is what gets stored instead of the token
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

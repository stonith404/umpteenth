//go:build unit

package crypto

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncryptRoundTrips(t *testing.T) {
	key, err := DeriveKey([]byte("a-master-key-long-enough"), "test")
	require.NoError(t, err)

	sealed, err := Encrypt(key, []byte("hello"))
	require.NoError(t, err)
	opened, err := Decrypt(key, sealed)
	require.NoError(t, err)
	require.Equal(t, "hello", string(opened))

	_, err = Decrypt(key, sealed[:5])
	require.Error(t, err)
}

func TestDecryptReadsStoredCiphertexts(t *testing.T) {
	// Secrets at rest are a 12-byte nonce followed by the sealed text, and this one was written before the switch to NewGCMWithRandomNonce
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	sealed, err := hex.DecodeString("a0a1a2a3a4a5a6a7a8a9aaab93750c5920ae6ccb0a45f4b66408a5aa03fe237d4ba16bad850d800915f52232")
	require.NoError(t, err)

	opened, err := Decrypt(key, sealed)
	require.NoError(t, err)
	require.Equal(t, "umpteenth secret", string(opened))
}

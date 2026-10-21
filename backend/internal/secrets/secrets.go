// Package secrets encrypts monitor credentials at rest.
//
// A monitor's database password (and HTTP auth secrets) sit in a JSONB column
// that ends up in every backup and every pg_dump. Encrypting them means a
// leaked dump is not a leaked credential, as long as ARGUS_SECRET_KEY lives
// somewhere else.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

// prefix marks a value as encrypted. A value without it is plaintext, which is
// what upgrades from before this existed look like.
const prefix = "enc:v1:"

var ErrNoKey = errors.New("ARGUS_SECRET_KEY is not set")

type Box struct {
	aead cipher.AEAD
}

// New takes 32 bytes as 64 hex characters. An empty key returns a nil Box,
// which stores secrets as they are.
func New(hexKey string) (*Box, error) {
	hexKey = strings.TrimSpace(hexKey)
	if hexKey == "" {
		return nil, nil
	}
	raw, err := hex.DecodeString(hexKey)
	if err != nil || len(raw) != 32 {
		return nil, errors.New("ARGUS_SECRET_KEY must be 64 hex characters (32 bytes)")
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Enabled reports whether encryption is configured.
func (b *Box) Enabled() bool { return b != nil }

// IsEncrypted reports whether a stored value is ciphertext.
func IsEncrypted(v string) bool { return strings.HasPrefix(v, prefix) }

// Seal encrypts a plaintext secret. Without a key, or for an empty or
// already-encrypted value, it returns the value unchanged.
func (b *Box) Seal(plain string) (string, error) {
	if plain == "" || IsEncrypted(plain) {
		return plain, nil
	}
	if b == nil {
		return plain, nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return prefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Open decrypts a stored secret. Plaintext passes through, so installs that
// predate encryption keep working until their monitors are saved again.
func (b *Box) Open(stored string) (string, error) {
	if !IsEncrypted(stored) {
		return stored, nil
	}
	if b == nil {
		return "", ErrNoKey
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(stored, prefix))
	if err != nil {
		return "", errors.New("stored secret is malformed")
	}
	n := b.aead.NonceSize()
	if len(raw) < n {
		return "", errors.New("stored secret is too short")
	}
	plain, err := b.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return "", errors.New("stored secret does not decrypt with this ARGUS_SECRET_KEY")
	}
	return string(plain), nil
}

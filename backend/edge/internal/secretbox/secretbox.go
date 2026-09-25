// Package secretbox encrypts small per-tenant secrets (webhook secrets, PSP
// key secrets) for storage at rest (D32).
//
// Format: "enc:v1:" + base64(nonce || AES-256-GCM ciphertext+tag).
// Key: CLEARLINE_SECRETS_KEY, standard base64 of exactly 32 bytes. It is a
// separate key from ZORD_VAULT_KEY (envelope payloads) so the two can be
// rotated independently. A missing or invalid key fails closed.
//
// Values without the "enc:v1:" prefix are legacy plaintext. Decrypt rejects
// them with ErrLegacyPlaintext; EnsureEncrypted is the idempotent helper that
// re-encrypts them.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// EnvKey is the env var that holds the base64-encoded 32-byte key.
const EnvKey = "CLEARLINE_SECRETS_KEY"

// Prefix marks a value sealed by this package (format version 1).
const Prefix = "enc:v1:"

var (
	ErrMissingKey      = errors.New("secretbox: " + EnvKey + " is not set")
	ErrInvalidKey      = errors.New("secretbox: " + EnvKey + " must be base64 of exactly 32 bytes")
	ErrLegacyPlaintext = errors.New("secretbox: stored secret is legacy plaintext (no enc:v1: prefix); run the re-encrypt helper")
	ErrMalformed       = errors.New("secretbox: stored secret is malformed")
	ErrDecrypt         = errors.New("secretbox: decryption failed (tampered value or wrong key)")
	ErrEmpty           = errors.New("secretbox: empty secret")
)

// Box seals and opens secrets with one AES-256-GCM key.
type Box struct {
	aead cipher.AEAD
}

// New builds a Box from a raw 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}
	return &Box{aead: aead}, nil
}

// FromEnv builds a Box from CLEARLINE_SECRETS_KEY. It is read on every call
// so a missing key is reported at the point of use, never silently skipped.
func FromEnv() (*Box, error) {
	raw := strings.TrimSpace(os.Getenv(EnvKey))
	if raw == "" {
		return nil, ErrMissingKey
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, ErrInvalidKey
	}
	return New(key)
}

// IsEncrypted reports whether stored carries the enc:v1: prefix.
func IsEncrypted(stored string) bool {
	return strings.HasPrefix(stored, Prefix)
}

// Encrypt seals plaintext with a fresh random nonce.
func (b *Box) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", ErrEmpty
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("secretbox: nonce: %w", err)
	}
	sealed := b.aead.Seal(nil, nonce, []byte(plaintext), nil)
	out := make([]byte, 0, len(nonce)+len(sealed))
	out = append(out, nonce...)
	out = append(out, sealed...)
	return Prefix + base64.StdEncoding.EncodeToString(out), nil
}

// Decrypt opens a value produced by Encrypt. Legacy plaintext is rejected.
func (b *Box) Decrypt(stored string) (string, error) {
	if stored == "" {
		return "", ErrEmpty
	}
	if !IsEncrypted(stored) {
		return "", ErrLegacyPlaintext
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, Prefix))
	if err != nil {
		return "", ErrMalformed
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns+b.aead.Overhead() {
		return "", ErrMalformed
	}
	plain, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", ErrDecrypt
	}
	return string(plain), nil
}

// EnsureEncrypted is the idempotent re-encrypt step: an enc:v1: value is
// verified (it must open with the current key) and returned unchanged; a
// legacy plaintext value is sealed. changed reports whether a write is needed.
func (b *Box) EnsureEncrypted(stored string) (out string, changed bool, err error) {
	if stored == "" {
		return "", false, ErrEmpty
	}
	if IsEncrypted(stored) {
		if _, err := b.Decrypt(stored); err != nil {
			return "", false, err
		}
		return stored, false, nil
	}
	sealed, err := b.Encrypt(stored)
	if err != nil {
		return "", false, err
	}
	return sealed, true, nil
}

// Encrypt seals plaintext with the key from CLEARLINE_SECRETS_KEY.
func Encrypt(plaintext string) (string, error) {
	b, err := FromEnv()
	if err != nil {
		return "", err
	}
	return b.Encrypt(plaintext)
}

// Decrypt opens stored with the key from CLEARLINE_SECRETS_KEY.
func Decrypt(stored string) (string, error) {
	b, err := FromEnv()
	if err != nil {
		return "", err
	}
	return b.Decrypt(stored)
}

// EnsureEncrypted runs Box.EnsureEncrypted with the key from the env.
func EnsureEncrypted(stored string) (string, bool, error) {
	b, err := FromEnv()
	if err != nil {
		return "", false, err
	}
	return b.EnsureEncrypted(stored)
}

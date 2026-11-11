// Package cryptox provides encryption and token helpers.
package cryptox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Key is an AES-256 key.
type Key struct{ key []byte }

func NewKey(key []byte) (*Key, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes")
	}
	return &Key{key: key}, nil
}

// Encrypt encrypts bytes with AES-256-GCM and returns a v1:-prefixed base64 value.
func (k *Key) Encrypt(plain []byte) (string, error) {
	block, err := aes.NewCipher(k.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, nonce, plain, nil)
	raw := append(nonce, sealed...)
	return "v1:" + base64.StdEncoding.EncodeToString(raw), nil
}

// Decrypt decrypts a v1 value.
func (k *Key) Decrypt(encoded string) ([]byte, error) {
	if !strings.HasPrefix(encoded, "v1:") {
		return nil, fmt.Errorf("unsupported ciphertext version")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encoded, "v1:"))
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plain, nil
}

// EncryptJSON encrypts a JSON value.
func (k *Key) EncryptJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return k.Encrypt(b)
}

// DecryptJSON decrypts into a destination.
func (k *Key) DecryptJSON(encoded string, dst any) error {
	b, err := k.Decrypt(encoded)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

// RandomToken returns a URL-safe random token.
func RandomToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken hashes a bearer/session/invite token for storage.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

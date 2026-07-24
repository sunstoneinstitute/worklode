// Package tokencrypt seals and opens small secrets (GitHub user tokens) with
// AES-256-GCM under a single 32-byte key supplied via LODE_TOKEN_ENC_KEY. The
// nonce is random per Seal and prepended to the ciphertext.
package tokencrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
)

// Cipher seals/opens byte slices under a fixed key.
type Cipher struct {
	aead cipher.AEAD
}

// New builds a Cipher from a 32-byte key. Any other length is an error.
func New(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("token encryption key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Seal returns nonce||ciphertext for plaintext.
func (c *Cipher) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Open reverses Seal. It errors on a too-short input or a failed auth tag.
func (c *Cipher) Open(blob []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(blob) < ns {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := blob[:ns], blob[ns:]
	pt, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	return pt, nil
}

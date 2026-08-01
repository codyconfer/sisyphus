// Package crypt is the AES-256-GCM sealing shared by the backup archive
// format and the sealed store. It is internal: applications go through
// sisyphus.Backup/Restore or the sealed package rather than sealing bytes
// themselves.
package crypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
)

// KeyBytes is the AES-256 key length.
const KeyBytes = 32

// NewKey returns a fresh random AES-256 key.
func NewKey() ([]byte, error) {
	k := make([]byte, KeyBytes)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	return k, nil
}

// Encrypt seals plaintext with AES-256-GCM, prefixing the random nonce.
func Encrypt(plaintext, key []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt opens a nonce-prefixed AES-256-GCM ciphertext.
func Decrypt(sealed, key []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(sealed) < ns {
		return nil, fmt.Errorf("ciphertext too short")
	}
	return gcm.Open(nil, sealed[:ns], sealed[ns:], nil)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != KeyBytes {
		return nil, fmt.Errorf("backup key must be %d bytes, got %d", KeyBytes, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

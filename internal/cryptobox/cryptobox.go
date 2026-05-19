// Package cryptobox provides AES-256-GCM authenticated encryption.
package cryptobox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
)

const nonceSize = 12

var ErrBadKey = errors.New("cryptobox: key must be exactly 32 bytes")

// Seal encrypts plaintext with a 32-byte key using AES-256-GCM.
// The random 12-byte nonce is prepended to the returned ciphertext.
func Seal(key, plaintext []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, ErrBadKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	ct := gcm.Seal(nonce, nonce, plaintext, nil)
	return ct, nil
}

// Open decrypts ciphertext (nonce prepended) produced by Seal.
func Open(key, ciphertext []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, ErrBadKey
	}
	if len(ciphertext) < nonceSize {
		return nil, errors.New("cryptobox: ciphertext too short")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := ciphertext[:nonceSize]
	return gcm.Open(nil, nonce, ciphertext[nonceSize:], nil)
}

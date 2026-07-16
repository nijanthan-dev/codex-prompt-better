// Package identity implements local keyed aliases without retaining identifiers.
package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
)

const keySize = 32

// Alias computes HMAC-SHA256 over a classified private identifier.
func Alias(key, identifier []byte) ([sha256.Size]byte, error) {
	if len(key) != keySize {
		return [sha256.Size]byte{}, errors.New("identity key must be 32 bytes")
	}
	if len(identifier) == 0 {
		return [sha256.Size]byte{}, errors.New("private identifier is required")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(identifier)
	var digest [sha256.Size]byte
	copy(digest[:], mac.Sum(nil))
	return digest, nil
}

// Verify checks an alias without exposing the private identifier.
func Verify(key, identifier, digest []byte) bool {
	expected, err := Alias(key, identifier)
	return err == nil && hmac.Equal(expected[:], digest)
}

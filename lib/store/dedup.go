package store

import (
	"crypto/sha256"
	"encoding/hex"
)

// ContentHash returns the sha256 hex (64 chars) of the canonical JSON.
func ContentHash(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

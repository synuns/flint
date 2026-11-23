package store

import (
	"crypto/sha256"
	"encoding/hex"
)

func ContentHash(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

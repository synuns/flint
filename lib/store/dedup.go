package store

import (
	"crypto/sha256"
	"encoding/hex"
)

// ContentHash는 canonical JSON의 sha256 hex(64자)를 반환한다.
func ContentHash(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

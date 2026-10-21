package store

import "testing"

// 플레이스홀더: T4가 실제 테스트로 대체한다. 구현 전에는 패닉으로 실패한다.
func TestNormalizeNotImplemented(t *testing.T) {
	_ = Normalize("knowledge", map[string]any{"tags": []string{"b", "a"}})
}

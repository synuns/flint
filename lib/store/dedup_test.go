package store

import "testing"

// 플레이스홀더: T5가 실제 테스트로 대체한다. 브리프 예제(동일 입력 -> 동일 해시) 검증.
func TestContentHashNotImplemented(t *testing.T) {
	if ContentHash(`{"a":1}`) != ContentHash(`{"a":1}`) {
		t.Fatal("ContentHash: 동일 입력에 다른 해시")
	}
}

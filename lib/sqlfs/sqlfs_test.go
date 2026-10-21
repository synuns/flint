package sqlfs

import "testing"

// 플레이스홀더: T1이 실제 테스트로 대체한다. 구현 전에는 패닉으로 실패한다.
func TestSQLFSStubsNotImplemented(t *testing.T) {
	_ = SchemaSQL()
	_ = ViewsSQL()
}

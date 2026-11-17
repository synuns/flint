// read_audit_commit_v1720_test.go — v1.7.2 T1(R38-C1): read 완료 경로 감사
// 커밋. Review Focus: TestReadCompletionAuditCommit — RunRead가 계측 커밋과
// 별개로 감사 커밋(st.Commit 'read: ' 접두)을 완료 경로에서 1회 수행한다
// (recall bump UPDATE knowledge·meta 증분의 감사 귀속 — telemetry -A 스윕
// 폐지 후 계측 커밋이 감사 변경을 흡수하지 않게 하는 회귀 방어).
package knowledge

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// TestReadCompletionAuditCommit — Review Focus(v1.7.2 T1): read 완료 경로의
// 감사 커밋. 실패는 조용해야 한다(§13 — 커밋 오류가 read exit를 깨지
// 않는다).
func TestReadCompletionAuditCommit(t *testing.T) {
	ctx := context.Background()
	t.Run("히트 — 감사 커밋 1회 'read: ' 접두", func(t *testing.T) {
		st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "FROM active_knowledge") {
				return hitRowsFixture, nil
			}
			return nil, nil
		}}
		var out bytes.Buffer
		if code := RunRead(ctx, st, []string{"백업 정책"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d: %s", code, out.String())
		}
		if len(st.commits) != 1 {
			t.Fatalf("감사 커밋 %d회, want 1(read 완료 경로)", len(st.commits))
		}
		if !strings.HasPrefix(st.commits[0], "read: ") {
			t.Fatalf("감사 커밋 메시지 = %q, want 'read: ' 접두", st.commits[0])
		}
	})
	t.Run("커밋 실패 — 조용히(§13 계측·비간섭)", func(t *testing.T) {
		st := &fakeStore{commitErr: errors.New("fake commit failure"), queryFn: func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "FROM active_knowledge") {
				return hitRowsFixture, nil
			}
			return nil, nil
		}}
		var out bytes.Buffer
		if code := RunRead(ctx, st, []string{"백업 정책"}, strings.NewReader(""), &out); code != 0 {
			t.Fatalf("exit = %d(커밋 실패가 read를 깨지 않는다): %s", code, out.String())
		}
	})
}

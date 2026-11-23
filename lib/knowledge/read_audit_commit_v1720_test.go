package knowledge

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

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

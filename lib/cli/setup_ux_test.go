// v0.17.0 R2 — setupUX 렌더 계약: TTY 강제 시 스텝·성공 요소 포함,
// non-TTY는 ANSI 코드 없는 plain 결과.
package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestSetupUXRenders(t *testing.T) {
	var b strings.Builder
	ux := newSetupUX(&b, true) // forceColor for test
	ux.Step("Doltgres 연결")
	ux.Success(":5434 (12ms)")
	ux.Step("마이그레이션")
	ux.Success("v5")
	s := b.String()
	for _, want := range []string{"Doltgres", "5434", "v5"} {
		if !strings.Contains(s, want) {
			t.Errorf("출력에 %q 부재:\n%s", want, s)
		}
	}
}

func TestSetupUXNonTTY(t *testing.T) {
	var b strings.Builder
	ux := newSetupUX(&b, false)
	ux.Step("테스트")
	ux.Success("완료")
	if strings.Contains(b.String(), "\033[") {
		t.Error("non-TTY에 ANSI 코드 존재")
	}
}

// TestSetupJSONMode — v0.20.1 재작성: 이 테스트는 이전 배선 결함(정상
// DB에서도 st=nil 고정 → 항상 degraded)을 '계약'으로 문서화하고 있었다
// (패치 루프 1차 F2 + 테스트 감사 축2). 새 계약: store 가용 시 ping·
// migrate가 실제로 돌아 ok. --json 한 줄·compose 1회·plain 동일성 불변.
func TestSetupJSONMode(t *testing.T) {
	withFake(t, &fakeStore{}) // Ping·Migrate 성공 fake — 건전 DB 경로.
	rec := withFakeCompose(t)
	out, code := mustRun(t, []string{"setup", "--json"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(건전 DB ok); out=%s", code, out)
	}
	if strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Fatalf("--json 출력은 한 줄이어야 한다: %q", out)
	}
	m := decode(t, out)
	if m["status"] != "ok" {
		t.Fatalf("status=%v, want ok; out=%s", m["status"], out)
	}
	if rec.upCalls != 1 {
		t.Fatalf("compose=%d회, want 1(동작 불변)", rec.upCalls)
	}
	// plain(non-TTY) 경로와 출력 완전 동일.
	plain, code2 := mustRun(t, []string{"setup"}, "")
	if code2 != code || strings.TrimSpace(plain) != strings.TrimSpace(out) {
		t.Fatalf("plain(--json 미지정, non-TTY) 불일치:\nplain=%q\njson =%q", plain, out)
	}
}

// TestSetupDegradedWhenStoreDown — DB 접속 불가(주 시나리오, T40)는
// st nil로 진행해 degraded·exit 1·next 안내(T40 계약 — v0.20.1 배선
// 수정 이후에도 nil 가드 경로 유지).
func TestSetupDegradedWhenStoreDown(t *testing.T) {
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		return nil, fmt.Errorf("db down")
	}
	t.Cleanup(func() { newStore = prev })
	withFakeCompose(t)
	out, code := mustRun(t, []string{"setup", "--json"}, "")
	if code != 1 {
		t.Fatalf("exit=%d, want 1(degraded); out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "degraded" {
		t.Fatalf("status=%v, want degraded; out=%s", m["status"], out)
	}
	if nexts, _ := m["next"].([]any); len(nexts) == 0 {
		t.Fatal("next 안내 유실")
	}
}

package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTruncateRunesSingleSource(t *testing.T) {
	long := strings.Repeat("한", 60)
	if got := truncateRunes(long, 40); len([]rune(got)) != 40 || got != strings.Repeat("한", 40) {
		t.Errorf("룬 절단 위반: %d룬", len([]rune(got)))
	}
	if got := truncateRunes("abc한", 3); got != "abc" {
		t.Errorf("혼합 문자열 절단=%q, want %q", got, "abc")
	}
	if got := truncateRunes("한글", 2); got != "한글" {
		t.Errorf("경계(상한==길이) 불변 위반: %q", got)
	}
	if got := truncateRunes("한글", 40); got != "한글" {
		t.Errorf("상한 이하 불변 위반: %q", got)
	}
	if got := truncateRunes("", 40); got != "" {
		t.Errorf("빈 문자열: %q", got)
	}

	if conflictSummaryPrefix(long) != truncateRunes(long, 40) {
		t.Errorf("conflictSummaryPrefix 미위임")
	}

	if oneLine(long, 48) != truncateRunes(long, 48) {
		t.Errorf("oneLine 미위임")
	}
}

func TestUnknownFlagExitContract(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}}
	out, code := runMaintainArgs(t, f, "--check", "--bogus")
	if code != 3 {
		t.Errorf("maintain 무효 플래그 exit=%d, want 3 (출력 %q)", code, out)
	}
	if !strings.Contains(out, "unknown flag") {
		t.Errorf("maintain 에러 미포함: %q", out)
	}

	fi := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	var b strings.Builder
	icode := runImport(context.Background(), fi, []string{"--verbose"}, strings.NewReader(""), &b)
	if icode != 3 {
		t.Errorf("import 무효 플래그 exit=%d, want 3 (출력 %q)", icode, b.String())
	}
	if !strings.Contains(b.String(), "unknown flag") {
		t.Errorf("import 에러 미포함: %q", b.String())
	}
}

func TestSupersedeZeroRowsFails(t *testing.T) {
	shortRetryDelays(t)
	f := &fakeMaint{supersedeErr: errors.New("supersede: id=3 갱신 미반영(경합 — 재시도)")}
	f.fakeStore = &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "SELECT (SELECT status") {
			return [][]any{{"active", time.Now().Add(-24 * time.Hour)}}, nil
		}
		return nil, errors.New("unexpected query: " + sql)
	}}
	out, code := runMaintainArgs(t, f, "--supersede", "3", "--by", "7")
	if code != 1 {
		t.Errorf("0행 갱신 exit=%d, want 1 (출력 %q)", code, out)
	}
	if !strings.Contains(out, "error") {
		t.Errorf("에러 JSON 아님: %q", out)
	}
	if len(f.commits) != 0 {
		t.Errorf("무음 성공 커밋 발생: %v", f.commits)
	}
}

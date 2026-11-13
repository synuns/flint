package backup

// R17-B1: Rotate는 flint 생성 덤프(flint-YYYYMMDD-HHMMSS-PID.sql|.age)만
// 로테이션 대상으로 삼는다 — 사용자가 같은 디렉터리에 둔 .sql/.age는
// 삭제도 keep 집계도 제외(데이터 손실 방지).

import (
	"path/filepath"
	"testing"
	"time"
)

// 계획 지정: flint 2개(구·신)+사용자 파일(최구 mtime) → keep 1 →
// 구 flint만 삭제, 사용자 파일은 잔존.
func TestRotateSkipsForeignDumps(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	writeDump(t, dir, "mine.sql", base.Add(-48*time.Hour), false) // 가장 구 mtime
	writeDump(t, dir, "flint-20260101-000000-1.sql", base.Add(-24*time.Hour), false)
	writeDump(t, dir, "flint-20260102-000000-2.sql", base, false) // 최신

	removed, err := Rotate(dir, 1)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	want := "flint-20260101-000000-1.sql"
	if len(removed) != 1 || removed[0] != want {
		t.Fatalf("removed=%v, want [%s] (사이드카 없음)", removed, want)
	}
	mustExist(t, filepath.Join(dir, "mine.sql"))
	mustExist(t, filepath.Join(dir, "flint-20260102-000000-2.sql"))
	mustGone(t, filepath.Join(dir, want))
}

// 계획 지정: 사용자 파일만 있으면 no-op — (nil, nil).
func TestRotateForeignOnlyNoop(t *testing.T) {
	dir := t.TempDir()
	writeDump(t, dir, "mine.sql", time.Now(), true)
	writeDump(t, dir, "manual.age", time.Now().Add(-time.Hour), false)

	removed, err := Rotate(dir, 1)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	if removed != nil {
		t.Fatalf("removed=%v, want nil", removed)
	}
	mustExist(t, filepath.Join(dir, "mine.sql"))
	mustExist(t, filepath.Join(dir, "mine.sql.sha256"))
	mustExist(t, filepath.Join(dir, "manual.age"))
}

// 패턴 경계: flint 생성 형식에서 한 글자라도 어긋면 사용자 파일로 취급.
func TestRotatePatternBoundaries(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	foreign := []string{
		"flint-202601-000000-1.sql",            // 날짜 7자리
		"flint-20260101-00000-1.sql",           // 시각 5자리
		"flint-20260101-000000-x.sql",          // PID 비숫자
		"flint-20260101-000000-.sql",           // PID 빈 문자열
		"flint-20260101-000000-1.txt",          // 확장자 불일치
		"flint-20260101-000000-1.sqlx",         // 확장자 접미
		"xflint-20260101-000000-1.sql",         // 접두
		"flint--20260101-000000-1.sql",         // 대시 중복
		"mybackup-flint-20260101-000000-1.sql", // 중간 삽입
	}
	for i, name := range foreign {
		writeDump(t, dir, name, base.Add(time.Duration(i)*time.Hour), false)
	}
	writeDump(t, dir, "flint-20260101-000000-42.age", base.Add(-100*time.Hour), false) // 대상(가장 구)
	writeDump(t, dir, "flint-20260113-235959-999.sql", base, false)                    // 대상(최신 — keep 1 점유)

	removed, err := Rotate(dir, 1)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	if len(removed) != 1 || removed[0] != "flint-20260101-000000-42.age" {
		t.Fatalf("removed=%v, want [flint-20260101-000000-42.age]", removed)
	}
	for _, name := range foreign {
		mustExist(t, filepath.Join(dir, name))
	}
}

// 계약 원천 정합: 실제 생산자(lazyDumpFile)는 암호화 덤프를
// flint-<date8>-<time6>-<pid>.sql.age로 짓는다(done.go ext=".sql.age").
// 패턴이 이 형식을 놓치면 암호화 백업이 영구 로테이션 제외(무한 축적).
func TestRotateMatchesEncryptedDumpName(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	enc := "flint-20261112-080000-77.sql.age"             // 생산 조립 형식 그대로
	writeDump(t, dir, enc, base.Add(-24*time.Hour), true) // .sql.age.sha256 사이드카
	writeDump(t, dir, "flint-20261113-090000-78.sql", base, false)

	removed, err := Rotate(dir, 1)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	if len(removed) != 2 || removed[0] != enc || removed[1] != enc+".sha256" {
		t.Fatalf("removed=%v, want [%s %s.sha256]", removed, enc, enc)
	}
	mustGone(t, filepath.Join(dir, enc))
	mustGone(t, filepath.Join(dir, enc+".sha256"))
	mustExist(t, filepath.Join(dir, "flint-20261113-090000-78.sql"))
}

// 사용자 파일이 keep 슬롯을 차지하지 않는다 — flint 3개+사용자 1개(최신)
// → keep 2여도 flint 1개가 삭제된다.
func TestRotateForeignDoesNotOccupyKeepSlot(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	writeDump(t, dir, "flint-20260101-000000-1.sql", base.Add(-3*time.Hour), false)
	writeDump(t, dir, "flint-20260102-000000-2.sql", base.Add(-2*time.Hour), false)
	writeDump(t, dir, "flint-20260103-000000-3.sql", base.Add(-1*time.Hour), false)
	writeDump(t, dir, "mine.sql", base, false) // 최신이지만 집계 제외

	removed, err := Rotate(dir, 2)
	if err != nil {
		t.Fatalf("Rotate 에러: %v", err)
	}
	if len(removed) != 1 || removed[0] != "flint-20260101-000000-1.sql" {
		t.Fatalf("removed=%v, want [flint-20260101-000000-1.sql]", removed)
	}
	mustExist(t, filepath.Join(dir, "mine.sql"))
}

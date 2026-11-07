// auto.go 는 하루 1회 자동 백업 트리거를 담당한다(PLAN v0.2 §8).
// 24h 경과 판정은 순수 함수로 분리하고, DB 접근은 meta 접근 함수를
// 주입받아 테스트 가능하게 한다. 실패는 에러 반환뿐 — 세션 종료를
// 막지 않는 비차단 처리는 호출부 책임이다.
package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultBackupIntervalH — 자동 백업 최소 주기 기본(시간). meta
// backup_interval_h 오버라이드(v0.24.0 R1c — backup_keep과 한 세트).
const DefaultBackupIntervalH = 24

// BackupIntervalKey — 주기 meta 키.
const BackupIntervalKey = "backup_interval_h"

// LastBackupKey 는 마지막 백업 시각을 저장하는 meta 키다.
const LastBackupKey = "last_backup"

// dumpFn 은 Dump 호출 seam이다(테스트에서 fake로 교체).
var dumpFn = Dump

func realDumpEnabled() bool { return os.Getenv("SUPERLITE_REAL") == "1" }

// ShouldBackup 은 마지막 백업(lastISO, RFC3339)으로부터 intervalH 시간이
// 경과했는지 판정한다. 비었거나 해석 불가능한 값은 첫 백업으로 보고
// true다. intervalH ≤ 0은 기본 24h 폴백.
func ShouldBackup(lastISO string, now time.Time, intervalH int) bool {
	if intervalH <= 0 {
		intervalH = DefaultBackupIntervalH
	}
	if lastISO == "" {
		return true
	}
	last, err := time.Parse(time.RFC3339, lastISO)
	if err != nil {
		return true
	}
	return now.Sub(last) >= time.Duration(intervalH)*time.Hour
}

// Auto 는 DSN 대상 자동 백업 실행기다.
type Auto struct {
	DSN string
}

// RunIfNeeded 는 last_backup 메타가 24h 이상 경과했을 때만 덤프를 dump에
// 기록하고 메타를 갱신한다. 비경과면 no-op, 실패면 에러를 반환한다.
// setKV가 실패하면 메타가 남지 않아 다음 호출에서 재시도된다.
func (a *Auto) RunIfNeeded(ctx context.Context,
	metaKV func(string) (string, error),
	setKV func(string, string) error,
	dump io.Writer) error {

	last, err := metaKV(LastBackupKey)
	if err != nil {
		return fmt.Errorf("backup auto: meta 읽기: %w", err)
	}
	intervalH := 0
	if v, err := metaKV(BackupIntervalKey); err == nil {
		if n, perr := strconv.Atoi(strings.TrimSpace(v)); perr == nil {
			intervalH = n
		}
	}
	now := time.Now().UTC()
	if !ShouldBackup(last, now, intervalH) {
		return nil
	}
	if err := dumpFn(ctx, a.DSN, dump); err != nil {
		return fmt.Errorf("backup auto: 덤프: %w", err)
	}
	return setKV(LastBackupKey, now.Format(time.RFC3339))
}

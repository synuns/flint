// rotate.go — 백업 로테이션(T54). .sql 덤프를 mtime 최신순으로 keep-N 유지,
// 나머지와 짝이루는 .sha256 사이드카를 함께 삭제한다(ADR-0006 사이드카 규약).
package backup

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BackupKeepKey — 로테이션 유지 수를 저장하는 meta 키다.
const BackupKeepKey = "backup_keep"

// DefaultBackupKeep — backup_keep 미설정·무효 시 유지 수 기본값.
const DefaultBackupKeep = 14

// BackupKeep 은 meta backup_keep 값을 읽어 유지 수를 반환한다.
// getKV는 키→값 조회 함수(auto.RunIfNeeded의 metaKV와 동일 형태).
// 에러·미설정·무효 값(비정수·1 미만)은 기본값 14로 폴백한다(에러 아님).
// e.g. BackupKeep(getKV) with backup_keep="3" -> 3, "0"/"x"/에러 -> 14
func BackupKeep(getKV func(string) (string, error)) int {
	if getKV == nil {
		return DefaultBackupKeep
	}
	v, err := getKV(BackupKeepKey)
	if err != nil {
		log.Printf("[flint] backup: %s 읽기 실패(%v) — 기본값 %d", BackupKeepKey, err, DefaultBackupKeep)
		return DefaultBackupKeep
	}
	n, perr := strconv.Atoi(strings.TrimSpace(v))
	if perr != nil || n < 1 {
		log.Printf("[flint] backup: %s=%q 무효 — 기본값 %d", BackupKeepKey, v, DefaultBackupKeep)
		return DefaultBackupKeep
	}
	return n
}

// Rotate 는 dir 안의 .sql 파일을 mtime 최신순으로 keep개 유지하고
// 나머지를 삭제한다. 삭제 대상 각각에 대해 짝이루는 <name>.sha256 사이드카가
// 있으면 함께 삭제하고(누락 시 .sql 단독), removed에 삭제한 basename을
// 기록한다(오래된 것부터: 원본 직후 사이드카). keep<1은 최소 1로 보정
// (전체 삭제 금지). dir 읽기 실패는 에러, 삭제 실패는 부분 목록+에러.
// e.g. Rotate(dir,1) with a.sql(new)·b.sql(old) -> removed=[b.sql b.sql.sha256]
func Rotate(dir string, keep int) ([]string, error) {
	if keep < 1 {
		keep = 1
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("backup rotate: %w", err)
	}

	type dump struct {
		name string
		mod  time.Time
	}
	var dumps []dump
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			return nil, fmt.Errorf("backup rotate: %s: %w", e.Name(), ierr)
		}
		dumps = append(dumps, dump{name: e.Name(), mod: info.ModTime()})
	}
	sort.Slice(dumps, func(i, j int) bool { return dumps[i].mod.After(dumps[j].mod) })
	if len(dumps) <= keep {
		return nil, nil
	}

	var removed []string
	for _, d := range dumps[keep:] { // 정렬 상 이후분은 오래된 순
		if rerr := os.Remove(filepath.Join(dir, d.name)); rerr != nil {
			return removed, fmt.Errorf("backup rotate: %w", rerr)
		}
		removed = append(removed, d.name)
		side := d.name + ".sha256"
		if _, serr := os.Stat(filepath.Join(dir, side)); serr == nil {
			if rerr := os.Remove(filepath.Join(dir, side)); rerr != nil {
				return removed, fmt.Errorf("backup rotate: %w", rerr)
			}
			removed = append(removed, side)
		}
	}
	return removed, nil
}

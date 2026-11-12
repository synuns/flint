// runbook_test.go — RUNBOOK.md에 운영 절차들과 doctor 검사 표 계약이
// 존재하는지 검증한다(T53·v0.34.2 T5 — 검사 16종 표·스키마 버전 표기).
package docs

import (
	"strings"
	"testing"
)

// TestRunbookSections — RUNBOOK.md에 audit·dolt_revert·drill·backup_keep
// 4키워드가 각 절에 포함되어 있는지 strings.Contains로 검증.
func TestRunbookSections(t *testing.T) {
	doc := read(t, "RUNBOOK.md")

	for _, kw := range []string{"audit", "dolt_revert", "drill", "backup_keep"} {
		if !strings.Contains(doc, kw) {
			t.Errorf("RUNBOOK.md에 %s 절 없음", kw)
		}
	}
}

// doctorCheckChain — lib/cli/doctor.go 실행 체인 순서(코어 6종 →
// tail-chain 10종). 표 순서 계약의 원천이다.
var doctorCheckChain = []string{
	"hook-file", "db", "schema", "integrity", "last-seen", "port",
	"backup-age", "conflicts", "zero-near", "pending-age",
	"event-volume", "backup-key", "meta-hygiene", "update",
	"recall-starved", "secrets",
}

// TestRunbookDoctorCheckTable — 검사 표가 16종 전부를 체인 순서대로
// 각 1행(이름·내용·수정 명령·warn/fail)으로 담는지 검증(v0.34.2 T5).
func TestRunbookDoctorCheckTable(t *testing.T) {
	doc := read(t, "RUNBOOK.md")

	if !strings.Contains(doc, "검사 16종") {
		t.Error("RUNBOOK.md에 '검사 16종' 없음 — 체인 총계(코어 6 + tail 10) 불일치")
	}
	last := -1
	for _, name := range doctorCheckChain {
		row := "| " + name + " |"
		i := strings.Index(doc, row)
		if i < 0 {
			t.Errorf("검사 표에 %s 행 없음", name)
			continue
		}
		if i < last {
			t.Errorf("%s 행이 체인 순서(doctor 실행 순서)보다 앞에 있음", name)
		}
		last = i
	}
}

// TestRunbookSchemaVersion — schema 검사 행이 현재 스키마 버전 v9를
// 기술하는지 검증(v0.34.2 T5 — lib/db LatestSchemaVersion=9 기준.
// v1.0.1 감사 F7: v8 고정 서술이 스키마 v9(v0.42.0) 이후 낡음).
func TestRunbookSchemaVersion(t *testing.T) {
	doc := read(t, "RUNBOOK.md")

	if !strings.Contains(doc, "현재 v9") {
		t.Error("RUNBOOK.md schema 행에 '현재 v9' 없음")
	}
	for _, stale := range []string{"현재 v8", "현재 v7"} {
		if strings.Contains(doc, stale) {
			t.Errorf("RUNBOOK.md에 낡은 '%s' 잔존", stale)
		}
	}
}

// runbook_test.go — RUNBOOK.md에 운영 절차들과 doctor 검사 표 계약이
// 존재하는지 검증한다(T53·v0.34.2 T5 — 검사 17종 표·스키마 버전 표기).
package doclint

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

// doctorCheckChain — lib/ops/doctor.go 등록표 실행 체인 순서(코어 6종 →
// tail-chain 14종). 표 순서 계약의 원천이다(v1.25.0 — dolt_log_depth·
// db_size·engine-footprint 3행 추가).
var doctorCheckChain = []string{
	"hook-file", "db", "schema", "integrity", "last-seen", "port",
	"backup-age", "conflicts", "zero-near", "pending-age",
	"event-volume", "backup-key", "meta-hygiene", "update",
	"recall-starved", "metering-freshness", "secrets",
	"dolt_log_depth", "db_size", "engine-footprint",
}

// TestRunbookDoctorCheckTable — 검사 표가 20종 전부를 체인 순서대로
// 각 1행(이름·내용·수정 명령·warn/fail)으로 담는지 검증(v0.34.2 T5).
func TestRunbookDoctorCheckTable(t *testing.T) {
	doc := read(t, "RUNBOOK.md")

	if !strings.Contains(doc, "검사 20종") {
		t.Error("RUNBOOK.md에 '검사 20종' 없음 — 체인 총계(코어 6 + tail 14) 불일치")
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

// TestRunbookSchemaVersion — schema 검사 행이 현재 스키마 버전 v10을
// 기술하는지 검증(v0.34.2 T5 — lib/db LatestSchemaVersion 기준.
// v1.0.1 감사 F7: v8 고정 서술이 스키마 v9(v0.42.0) 이후 낡음.
// v1.2.0 T6: 스키마 v10(read_events 열 3종·knowledge_reads) 갱신).
func TestRunbookSchemaVersion(t *testing.T) {
	doc := read(t, "RUNBOOK.md")

	if !strings.Contains(doc, "현재 v10") {
		t.Error("RUNBOOK.md schema 행에 '현재 v10' 없음")
	}
	for _, stale := range []string{"현재 v9", "현재 v8", "현재 v7"} {
		if strings.Contains(doc, stale) {
			t.Errorf("RUNBOOK.md에 낡은 '%s' 잔존", stale)
		}
	}
}

// TestRunbookEngineOpsSections — the v1.25.0 engine-ops sections exist:
// §10 garbage collection (the disconnect contract, the engine-auto-GC-first
// principle, the quiet-hours rule, the post-run doctor/status check) and
// §11 engine image upgrade (the fixed order backup → restore --drill →
// image update → up -d → doctor).
func TestRunbookEngineOpsSections(t *testing.T) {
	doc := read(t, "RUNBOOK.md")

	for _, kw := range []string{
		"## 10. 가비지 컬렉션",
		"maintain --gc --yes",
		"접속 단절",
		"자동 GC",
		"조용한 시간대",
		"## 11. 엔진 이미지 업그레이드",
		"restore --file <덤프> --drill",
		"docker compose up -d",
	} {
		if !strings.Contains(doc, kw) {
			t.Errorf("RUNBOOK.md에 %q 없음", kw)
		}
	}
	// §10 precedes §11, and within §10 the post-run verification names both
	// doctor and status (the spec M1 §10 contract).
	i10 := strings.Index(doc, "## 10. 가비지 컬렉션")
	i11 := strings.Index(doc, "## 11. 엔진 이미지 업그레이드")
	if i10 < 0 || i11 < 0 || i10 > i11 {
		t.Fatalf("§10·§11 순서 위반(§10=%d, §11=%d)", i10, i11)
	}
	if end := strings.Index(doc[i10:], "## 11."); end < 0 {
		t.Fatal("§10 본문 없음")
	} else if sec := doc[i10 : i10+end]; !strings.Contains(sec, "flint doctor") || !strings.Contains(sec, "flint status") {
		t.Errorf("§10 실행 후 확인(doctor·status) 부재: %q", sec)
	}
	// §11 — the fixed order backup → drill → image update → up -d → doctor
	// (each step precedes the next inside the section).
	end11 := strings.Index(doc[i11:], "\n## ")
	if end11 < 0 {
		end11 = len(doc[i11:])
	}
	sec11 := doc[i11 : i11+end11]
	last := -1
	for _, kw := range []string{"backup", "--drill", "이미지 갱신", "up -d", "doctor"} {
		i := strings.Index(sec11, kw)
		if i < 0 {
			t.Fatalf("§11에 %q 없음", kw)
		}
		if i < last {
			t.Errorf("§11 순서 위반: %q(idx=%d < prev=%d)", kw, i, last)
		}
		last = i
	}
}

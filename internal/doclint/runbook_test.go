package doclint

import (
	"strings"
	"testing"
)

func TestRunbookSections(t *testing.T) {
	doc := read(t, "RUNBOOK.md")

	for _, kw := range []string{"audit", "dolt_revert", "drill", "backup_keep"} {
		if !strings.Contains(doc, kw) {
			t.Errorf("RUNBOOK.md에 %s 절 없음", kw)
		}
	}
}

var doctorCheckChain = []string{
	"hook-file", "db", "schema", "integrity", "last-seen", "port",
	"backup-age", "conflicts", "zero-near", "pending-age",
	"event-volume", "backup-key", "meta-hygiene", "update",
	"recall-starved", "metering-freshness", "secrets",
	"dolt_log_depth", "db_size", "engine-footprint",
}

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

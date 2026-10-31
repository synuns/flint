package docs

import (
	"os"
	"strings"
	"testing"
)

func TestTuningRecordsV0101Constants(t *testing.T) {
	b, err := os.ReadFile("TUNING.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"backupAgeWarn", "72", "suggestMinScore", "0.15"} {
		if !strings.Contains(s, want) {
			t.Errorf("TUNING.md에 %q 누락", want)
		}
	}
}

// TestTuningBackupAgeWarnRationaleMatchesCode — 근거 문장이 doctor의
// warn 조건(age >= backupAgeWarn)과 모순되지 않는지: 2회 연속 실패는
// 경고 유예, 3회째부터 경고임을 명시해야 한다.
func TestTuningBackupAgeWarnRationaleMatchesCode(t *testing.T) {
	b, err := os.ReadFile("TUNING.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "2회 연속 실패까지는 경고하지 않고") {
		t.Errorf("backupAgeWarn 근거가 2회 실패 시 경고 유예를 명시하지 않음 — 코드(doctor: age >= 72h warn)와 모순되는 서술로 퇴행했는지 확인")
	}
}

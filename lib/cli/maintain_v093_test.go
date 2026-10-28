// maintain_v093_test.go — T3 `maintain --dup-suspects` 보고(보고 전용·exit 0).
package cli

import (
	"errors"
	"strings"
	"testing"

	"flint/lib/db"
)

func TestDupSuspectsJSONFormat(t *testing.T) {
	got := dupSuspectsJSON([]db.DupGroup{{Topic: "rv-e", IDs: []int{63, 64}}})
	want := `{"status":"dup-suspects","groups":[{"topic":"rv-e","ids":[63,64]}]}`
	if got != want {
		t.Errorf("dupSuspectsJSON = %s, want %s", got, want)
	}
}

func TestDupSuspectsJSONEmpty(t *testing.T) {
	if got := dupSuspectsJSON(nil); got != `{"status":"dup-suspects","groups":[]}` {
		t.Errorf("빈 그룹 = %s", got)
	}
}

// TestMaintainDupSuspectsFlag — Review Focus(명명): `--dup-suspects --limit 5`
// 플래그 파싱(limit 전달·기본 10)·출력 JSON·usage 라인 갱신.
func TestMaintainDupSuspectsFlag(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{},
		dupGroups: []db.DupGroup{{Topic: "rv-e", IDs: []int{63, 64}}}}
	out, code := runMaintainArgs(t, f, "--dup-suspects", "--limit", "5")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `{"status":"dup-suspects","groups":[{"topic":"rv-e","ids":[63,64]}]}`) {
		t.Errorf("출력 %q", out)
	}
	if len(f.dupGroupsCalls) != 1 || f.dupGroupsCalls[0] != 5 {
		t.Errorf("DupGroups 호출 limit = %v, want [5]", f.dupGroupsCalls)
	}

	// 기본 limit 10(스펙 R4: --limit M 기본 10).
	f2 := &fakeMaint{fakeStore: &fakeStore{}}
	out2, code2 := runMaintainArgs(t, f2, "--dup-suspects")
	if code2 != 0 {
		t.Fatalf("기본 limit: exit=%d, want 0 (출력 %q)", code2, out2)
	}
	if len(f2.dupGroupsCalls) != 1 || f2.dupGroupsCalls[0] != 10 {
		t.Errorf("기본 limit 호출 = %v, want [10]", f2.dupGroupsCalls)
	}

	// usage 라인 갱신 — 모드 누락 에러 안내에 --dup-suspects 포함.
	u := &fakeMaint{fakeStore: &fakeStore{}}
	uout, ucode := runMaintainArgs(t, u)
	if ucode != 1 || !strings.Contains(uout, "--dup-suspects") {
		t.Errorf("usage 갱신 누락: exit=%d %q", ucode, uout)
	}

	// 조회 실패 — errLine 전파·MapExit 코드.
	e := &fakeMaint{fakeStore: &fakeStore{}, dupGroupsErr: errors.New("boom")}
	eout, ecode := runMaintainArgs(t, e, "--dup-suspects")
	if ecode == 0 || !strings.Contains(eout, "boom") {
		t.Errorf("에러 미전파: exit=%d %q", ecode, eout)
	}

	// 모드 귀속 — --id는 --dup-suspects와 함께 쓸 수 없다.
	m := &fakeMaint{fakeStore: &fakeStore{}}
	mout, mcode := runMaintainArgs(t, m, "--dup-suspects", "--id", "7", "--reason", "x")
	if mcode != 1 || !strings.Contains(mout, `"error"`) {
		t.Errorf("무관 플래그 거부 없음: exit=%d %q", mcode, mout)
	}
}

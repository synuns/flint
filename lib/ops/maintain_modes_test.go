package ops

import "testing"

var maintainModeFlagContract = map[string][]string{
	"--check":             {},
	"--meta-audit":        {},
	"--prune-invalidated": {},
	"--dup-suspects":      {"--limit"},
	"--conflict-suspects": {"--limit"},
	"--stale":             {"--days", "--limit"},
	"--invalidate-stale":  {"--days", "--limit"},
	"--invalidate":        {"--id", "--reason", "--ids"},
	"--merge":             {"--from", "--to"},
	"--supersede":         {"--supersede", "--by"},
	"--expire-pending":    {},
	"--decay-audit":       {"--limit"},
	"--stale-state":       {},
	"--age-audit":         {"--limit", "--apply-vol"},
	"--merge-audit":       {"--limit"},
	"--recall-audit":      {"--limit"},
	"--reaffirm":          {"--id", "--ids"},
	"--recheck":           {"--conditions"},
	"--gc":                {},
	"--purge-dropped":     {},
}

var maintainYesModeContract = map[string]bool{
	"--invalidate": true, "--invalidate-stale": true, "--reaffirm": true,
	"--gc": true, "--purge-dropped": true,
}

var maintainCommitModeContract = map[string]bool{
	"--expire-pending": true, "--prune-invalidated": true, "--stale-state": true,
}

var maintainValueModeContract = map[string]bool{
	"--expire-pending": true, "--prune-invalidated": true, "--recheck": true,
}

func TestMaintainModeRegistryContract(t *testing.T) {
	modes := maintainModes()
	if len(modes) != len(maintainModeFlagContract) {
		t.Fatalf("등록 모드 수 = %d, want %d(기존 maintainModeFlags 전체)",
			len(modes), len(maintainModeFlagContract))
	}
	seen := map[string]bool{}
	for _, m := range modes {
		if m.name == "" {
			t.Fatal("빈 모드명 등록")
		}
		if seen[m.name] {
			t.Fatalf("중복 등록: %s", m.name)
		}
		seen[m.name] = true
		want, ok := maintainModeFlagContract[m.name]
		if !ok {
			t.Fatalf("계약 밖 모드 등록: %s", m.name)
		}
		got := map[string]bool{}
		for _, f := range m.flags {
			if got[f] {
				t.Fatalf("%s: 플래그 중복 %s", m.name, f)
			}
			got[f] = true
		}
		for _, f := range want {
			if !got[f] {
				t.Fatalf("%s: 플래그 세트 불일치 — %s 부재(got %v, want %v)",
					m.name, f, m.flags, want)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("%s: 플래그 수 불일치 — got %v, want %v", m.name, m.flags, want)
		}
		if m.run == nil {
			t.Fatalf("%s: run 핸들러 미등록", m.name)
		}
		if m.yes != maintainYesModeContract[m.name] {
			t.Fatalf("%s: yes = %v, want %v", m.name, m.yes, maintainYesModeContract[m.name])
		}
		if m.commit != maintainCommitModeContract[m.name] {
			t.Fatalf("%s: commit = %v, want %v", m.name, m.commit, maintainCommitModeContract[m.name])
		}
		if m.value != maintainValueModeContract[m.name] {
			t.Fatalf("%s: value = %v, want %v", m.name, m.value, maintainValueModeContract[m.name])
		}
	}

	for name := range maintainModeFlagContract {
		if !seen[name] {
			t.Fatalf("기존 모드 미등록: %s", name)
		}
		if _, ok := maintainModeSpec(name); !ok {
			t.Fatalf("maintainModeSpec(%q) 조회 실패", name)
		}
	}
	if _, ok := maintainModeSpec("--nope"); ok {
		t.Fatal("maintainModeSpec 미등록 모드 조회 성공")
	}
}

func TestMaintainModeDerivedMaps(t *testing.T) {
	for name, want := range maintainModeFlagContract {
		allowed, ok := maintainModeFlags[name]
		if !ok {
			t.Fatalf("maintainModeFlags에 %s 부재", name)
		}
		for _, f := range want {
			if !allowed[f] {
				t.Fatalf("maintainModeFlags[%s][%s] = false, want true", name, f)
			}
		}
		if len(allowed) != len(want) {
			t.Fatalf("maintainModeFlags[%s] 크기 = %d, want %d", name, len(allowed), len(want))
		}
	}
	if len(maintainModeFlags) != len(maintainModeFlagContract) {
		t.Fatalf("maintainModeFlags 항목 수 = %d, want %d", len(maintainModeFlags), len(maintainModeFlagContract))
	}
	for name := range maintainYesModeContract {
		if !maintainYesModes[name] {
			t.Fatalf("maintainYesModes[%s] = false", name)
		}
	}
	for name := range maintainCommitModeContract {
		if !maintainCommitModes[name] {
			t.Fatalf("maintainCommitModes[%s] = false", name)
		}
	}
}

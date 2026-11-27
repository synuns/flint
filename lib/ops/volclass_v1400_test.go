package ops

import "testing"

func TestForSourceHumanImmutable(t *testing.T) {
	if got := SuggestVolClassForSource("회로 정리", "", "human"); got != VolClassImmutable {
		t.Errorf("human: SuggestVolClassForSource(\"회로 정리\", \"\", \"human\") = %q, want %q", got, VolClassImmutable)
	}
	if got := SuggestVolClassForSource("회로 정리", "", "correction"); got != VolClassImmutable {
		t.Errorf("correction: SuggestVolClassForSource(\"회로 정리\", \"\", \"correction\") = %q, want %q", got, VolClassImmutable)
	}
	if got := SuggestVolClassForSource("버전 경로 명령", "", "human"); got != VolClassImmutable {
		t.Errorf("human volatile-keyword: got %q, want %q", got, VolClassImmutable)
	}
}

func TestForSourceImportConditional(t *testing.T) {
	if got := SuggestVolClassForSource("adr 결정", "", "import"); got != VolClassConditional {
		t.Errorf("import: SuggestVolClassForSource(\"adr 결정\", \"\", \"import\") = %q, want %q", got, VolClassConditional)
	}
	if got := SuggestVolClassForSource("버전 경로", "", "vault"); got != VolClassConditional {
		t.Errorf("vault: got %q, want %q", got, VolClassConditional)
	}
}

func TestForSourceAgentKeepsRules(t *testing.T) {
	cases := []struct {
		topic, tags string
	}{
		{"회로 정리", ""},
		{"adr 결정 기록", ""},
		{"버전 경로 명령 도구", ""},
		{"t4-ADR 버전 기록", ":version:"},
		{"t4-결정 경로 명령", ""},
		{"일반 주제", ":project:"},
	}
	for _, c := range cases {
		for _, src := range []string{"agent", ""} {
			want := SuggestVolClass(c.topic, c.tags)
			if got := SuggestVolClassForSource(c.topic, c.tags, src); got != want {
				t.Errorf("source %q: SuggestVolClassForSource(%q, %q) = %q, want %q (= SuggestVolClass)", src, c.topic, c.tags, got, want)
			}
		}
	}
}

func TestClaimDefaultVolTable(t *testing.T) {
	cases := []struct {
		claim string
		want  string
	}{
		{"decision", VolClassImmutable},
		{"procedure", VolClassConditional},
		{"lesson", VolClassConditional},
		{"event", VolClassImmutable},
		{"", VolClassConditional},
		{"unknown-claim", VolClassConditional},
	}
	for _, c := range cases {
		if got := ClaimDefaultVol(c.claim); got != c.want {
			t.Errorf("ClaimDefaultVol(%q) = %q, want %q", c.claim, got, c.want)
		}
	}
}

package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/wire"
)

const (
	diffFromHash = "aaaa1111aaaa1111aaaa1111aaaa1111"
	diffToHash   = "bbbb2222bbbb2222bbbb2222bbbb2222"
)

type diffCliFake struct {
	*fakeStore
	logPrefixes []string
}

func (f *diffCliFake) LogEntries(ctx context.Context, q wire.LogQuery) ([]wire.LogEntry, error) {
	if q.Prefix == "" {
		return nil, nil
	}
	f.logPrefixes = append(f.logPrefixes, q.Prefix)
	switch {
	case strings.HasPrefix(diffFromHash, q.Prefix):
		return []wire.LogEntry{{Hash: diffFromHash}}, nil
	case strings.HasPrefix(diffToHash, q.Prefix):
		return []wire.LogEntry{{Hash: diffToHash}}, nil
	}
	return nil, nil
}

func (f *diffCliFake) DiffRows(ctx context.Context, fromHash, toHash string) ([]wire.KnowledgeDiffRow, error) {

	return []wire.KnowledgeDiffRow{
		{DiffType: "added", ID: "7", Topic: "new-topic"},
		{DiffType: "added", ID: "8", Topic: "newer-topic"},
		{DiffType: "modified", ID: "3", Topic: "changed-topic"},
		{DiffType: "removed", ID: "5", Topic: "gone-topic"},
	}, nil
}

func (f *diffCliFake) DiffCommits(ctx context.Context, fromHash, toHash string) ([]wire.DiffCommitSummary, error) {
	return []wire.DiffCommitSummary{{Hash: diffToHash, Date: time.Now(), Message: "second write", DataChange: true}}, nil
}

func TestDiffAddedCounts(t *testing.T) {
	f := &diffCliFake{fakeStore: &fakeStore{}}
	withFakeStore(t, f)
	out, code := mustRun(t, []string{"diff", "aaaa1", "bbbb2"}, "")
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out)
	}
	m := decode(t, out)
	if m["from"] != diffFromHash || m["to"] != diffToHash {
		t.Fatalf("from/to 해시 정규화 실패: %v", m)
	}
	for k, want := range map[string]float64{"added": 2, "modified": 1, "removed": 1} {
		if got, _ := m[k].(float64); got != want {
			t.Fatalf("%s=%v want %v", k, m[k], want)
		}
	}
	rows, _ := m["rows"].([]any)
	if len(rows) != 4 {
		t.Fatalf("rows len=%d want 4", len(rows))
	}
	first, _ := rows[0].(map[string]any)
	if first["diff_type"] != "added" || first["id"] != "7" {
		t.Fatalf("rows[0]=%v", rows[0])
	}
	commits, _ := m["commits"].([]any)
	if len(commits) != 1 {
		t.Fatalf("commits len=%d want 1", len(commits))
	}
	cm, _ := commits[0].(map[string]any)
	if cm["hash"] != diffToHash || cm["data_change"] != true {
		t.Fatalf("commits[0]=%v", cm)
	}
}

func TestDiffSummaryShape(t *testing.T) {
	f := &diffCliFake{fakeStore: &fakeStore{}}
	withFakeStore(t, f)
	out, code := mustRun(t, []string{"diff", "aaaa1", "bbbb2", "--summary"}, "")
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out)
	}
	m := decode(t, out)
	if _, ok := m["rows"]; ok {
		t.Fatalf("--summary에 rows 포함: %s", out)
	}
	for _, k := range []string{"from", "to", "added", "modified", "removed", "commits"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("키 %s 부재: %s", k, out)
		}
	}
	commits, _ := m["commits"].([]any)
	cm, _ := commits[0].(map[string]any)
	for _, k := range []string{"hash", "date", "message", "data_change", "schema_change"} {
		if _, ok := cm[k]; !ok {
			t.Fatalf("commits[0] 키 %s 부재: %v", k, cm)
		}
	}
}

func TestDiffInvalidExit3(t *testing.T) {
	f := &diffCliFake{fakeStore: &fakeStore{}}
	withFakeStore(t, f)
	cases := [][]string{
		{"diff", "2 days ago", "bbbb2"},
		{"diff", "aaaa1", "zzz9"},
		{"diff", "aaaa1"},
	}
	for _, args := range cases {
		out, code := mustRun(t, args, "")
		if code != 3 {
			t.Fatalf("%v exit=%d want 3 out=%s", args, code, out)
		}
		m := decode(t, out)
		if errKind, _ := m["error"].(string); errKind != "invalid_as_of" &&
			!strings.HasPrefix(errKind, "usage") {
			t.Fatalf("%v error=%v", args, m["error"])
		}
	}
}

func TestDiffCeilingHintExit3(t *testing.T) {
	f := &diffCliFake{fakeStore: &fakeStore{}}
	withFakeStore(t, f)
	out, code := mustRun(t, []string{"diff", "4000000h", "bbbb2"}, "")
	if code != 3 {
		t.Fatalf("exit=%d want 3 out=%s", code, out)
	}
	m := decode(t, out)
	if errKind, _ := m["error"].(string); errKind != "invalid_as_of" {
		t.Fatalf("error=%v, want invalid_as_of", m["error"])
	}
	if msg, _ := m["message"].(string); !strings.Contains(msg, "exceeds") || !strings.Contains(msg, "ceiling") {
		t.Fatalf("message에 상한 문구 없음: %q", msg)
	}
}

func TestDiffPrefixParameterized(t *testing.T) {
	f := &diffCliFake{fakeStore: &fakeStore{}}
	withFakeStore(t, f)
	out, code := mustRun(t, []string{"diff", "aaaa1", "bbbb2"}, "")
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out)
	}
	if len(f.logPrefixes) != 2 || f.logPrefixes[0] != "aaaa1" || f.logPrefixes[1] != "bbbb2" {
		t.Fatalf("접두어 인자 기록: %v", f.logPrefixes)
	}
}

func TestDiffSeamReal(t *testing.T) {
	dsn, ok := seamRealDSN()
	if !ok {
		t.Skip("실구현 seam 검증은 SUPERLITE_REAL=1 + FLINT_TEST_DSN 설정 시만")
	}
	f := &fakeStore{}
	withFake(t, f)
	if out, code := mustRun(t, []string{"write", "knowledge", "--session", "diff-seam"},
		`{"topic":"diff-seam-a","summary":"seam a","confidence":0.5}`); code != 0 {
		t.Fatalf("write A exit=%d out=%s", code, out)
	}
	if out, code := mustRun(t, []string{"write", "knowledge", "--session", "diff-seam"},
		`{"topic":"diff-seam-b","summary":"seam b","confidence":0.6}`); code != 0 {
		t.Fatalf("write B exit=%d out=%s", code, out)
	}

	ctx := context.Background()
	real, err := newRealStore(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	defer real.Close()
	entries, err := real.LogEntries(ctx, wire.LogQuery{
		LatestN: 2, MessageLike: `%[session:diff-seam]%`,
	})
	if err != nil || len(entries) != 2 {
		t.Fatalf("세션 커밋 조회 실패: entries=%d err=%v", len(entries), err)
	}
	latestHash, prevHash := entries[0].Hash, entries[1].Hash
	latestDate, prevDate := entries[0].Date, entries[1].Date
	out, code := mustRun(t, []string{"diff", prevDate.Format(time.RFC3339Nano), latestDate.Format(time.RFC3339Nano)}, "")
	if code != 0 {
		t.Fatalf("diff exit=%d out=%s", code, out)
	}
	m := decode(t, out)
	if m["from"] != prevHash || m["to"] != latestHash {
		t.Fatalf("from/to 불일치: %s", out)
	}
	if added, _ := m["added"].(float64); added < 1 {
		t.Fatalf("added=%v want ≥1: %s", m["added"], out)
	}

	out2, code2 := mustRun(t, []string{"diff", latestDate.Format(time.RFC3339Nano), latestDate.Format(time.RFC3339Nano)}, "")
	if code2 != 0 {
		t.Fatalf("동일 지점 exit=%d out=%s", code2, out2)
	}
	m2 := decode(t, out2)
	if m2["added"].(float64) != 0 || m2["removed"].(float64) != 0 {
		t.Fatalf("동일 지점 diff가 0이 아님: %s", out2)
	}
}

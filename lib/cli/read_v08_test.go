package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/wire"
)

func withFakeStore(t *testing.T, st Store) {
	t.Helper()
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) { return st, nil }
	t.Cleanup(func() { newStore = prev })
}

func readAsofStoreOrReal(t *testing.T, st Store) Store {
	t.Helper()
	return st
}

const asofHash = "he08irluqn3v2m1k9x7c4jw5z6b8d0f"

var asofCommitDate = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

type readAsofFake struct {
	*fakeStore
	asofQueries []wire.AsOfQuery
	zeroHit     bool
	asofErr     error
}

func (f *readAsofFake) LogEntries(ctx context.Context, q wire.LogQuery) ([]wire.LogEntry, error) {
	switch {
	case q.Prefix != "":
		if strings.HasPrefix(asofHash, q.Prefix) {
			return []wire.LogEntry{{Hash: asofHash}}, nil
		}
		return nil, nil
	case !q.BeforeOrAt.IsZero():
		return []wire.LogEntry{{Hash: asofHash}}, nil
	case q.Hash == asofHash:
		return []wire.LogEntry{{Hash: asofHash, Date: asofCommitDate}}, nil
	}
	return nil, nil
}

func (f *readAsofFake) QueryAsOf(ctx context.Context, q wire.AsOfQuery) ([][]any, error) {
	f.asofQueries = append(f.asofQueries, q)
	if f.asofErr != nil {
		return nil, f.asofErr
	}
	if f.zeroHit {
		return nil, nil
	}
	return [][]any{{int32(3), "토픽", "요약", ":a:", nil, "main", 0.9, "active"}}, nil
}

func asofFakeOrReal(t *testing.T, f *fakeStore) Store {
	t.Helper()
	if dsn, real := seamRealDSN(); real {
		realStore, err := newRealStore(context.Background(), dsn)
		if err != nil {
			t.Skipf("real store unavailable: %v", err)
		}
		t.Cleanup(realStore.Close)
		return &delegatingFake{fake: f, real: realStore}
	}
	return f
}

func asofReadSQL(f *fakeStore) string {
	for _, q := range f.queries {
		if strings.Contains(q, "FROM active_knowledge") {
			return q
		}
	}
	return ""
}

func TestReadAsOfAddsField(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &readAsofFake{fakeStore: &fakeStore{}}
	if code := runRead(ctx, readAsofStoreOrReal(t, f), []string{"--as-of", "7d", "토픽"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runRead exit = %d", code)
	}
	if len(f.asofQueries) != 1 {
		t.Fatalf("과거 스냅샷 질의 %d건, want 1", len(f.asofQueries))
	}
	aq := f.asofQueries[0]
	if aq.Hash != asofHash || aq.Table != "active_knowledge" {
		t.Errorf("해시 접합 실패: %+v", aq)
	}

	if !strings.Contains(aq.Where, "ORDER BY (lower(topic)=lower($2)) DESC") {
		t.Errorf("RankOrder 절 변형: %q", aq.Where)
	}
	if aq.Limit <= 0 {
		t.Errorf("LIMIT 유실: %d", aq.Limit)
	}

	got := out.String()
	if !strings.HasPrefix(got, "3|토픽|요약\n") {
		t.Errorf("기존 행 출력 변형: %q", got)
	}
	if !strings.Contains(got, `"as_of":{"hash":"he08irluqn3v2m1k9x7c4jw5z6b8d0f","commit_date":"2026-10-20T12:00:00Z"}`) {
		t.Errorf("as_of 필드 누락/불일치: %q", got)
	}

	var out2 bytes.Buffer
	f2 := &fakeStore{}
	if code := runRead(ctx, asofFakeOrReal(t, f2), []string{"토픽"}, strings.NewReader(""), &out2); code != 0 {
		t.Fatalf("미지정 exit = %d", code)
	}
	if strings.Contains(out2.String(), "as_of") {
		t.Errorf("미지정인데 as_of 출력: %q", out2.String())
	}
}

func TestReadAsOfInvalidExit3(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &readAsofFake{fakeStore: &fakeStore{}}
	code := runRead(ctx, readAsofStoreOrReal(t, f), []string{"--as-of", "2 days ago", "토픽"}, strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("exit = %d", code)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"error":"invalid_as_of"`) {
		t.Errorf("한 줄 JSON invalid_as_of 여부: %q", out.String())
	}
	if len(f.asofQueries) != 0 {
		t.Errorf("무효 스펙인데 과거 스냅샷 질의 실행: %+v", f.asofQueries)
	}
}

func TestReadAsOfLimitRegression(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &readAsofFake{fakeStore: &fakeStore{}}
	if code := runRead(ctx, readAsofStoreOrReal(t, f), []string{"--as-of", "7d", "토픽", "--limit", "3"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if len(f.asofQueries) != 1 {
		t.Fatalf("과거 스냅샷 질의 %d건, want 1", len(f.asofQueries))
	}
	aq := f.asofQueries[0]
	if aq.Hash != asofHash {
		t.Errorf("해시 접합 유실: %+v", aq)
	}

	if !strings.Contains(aq.Where, "LIKE lower($1)") || !strings.Contains(aq.Where, "lower($2)") {
		t.Errorf("q LIKE·랭킹 절 변형: %q", aq.Where)
	}
	if aq.Limit != 3 {
		t.Errorf("Limit=%d, want 3", aq.Limit)
	}
}

func TestReadAsOfMissingValueExit3(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &readAsofFake{fakeStore: &fakeStore{}}
	code := runRead(ctx, readAsofStoreOrReal(t, f), []string{"--as-of"}, strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("exit = %d, want 3; out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), `"error":"invalid_as_of"`) {
		t.Errorf("invalid_as_of 한 줄 JSON 아님: %q", out.String())
	}
	if len(f.asofQueries) != 0 {
		t.Errorf("무효 스펙인데 과거 스냅샷 질의 실행: %+v", f.asofQueries)
	}
}

func TestReadAsOfViewColumnHint(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &readAsofFake{fakeStore: &fakeStore{}}
	f.asofErr = errors.New(`view "active_knowledge" incompatible at this commit (column "confidence" does not exist) — retry with base table "knowledge"`)
	code := runRead(ctx, readAsofStoreOrReal(t, f), []string{"--as-of", "7d"}, strings.NewReader(""), &out)
	if code == 0 {
		t.Fatalf("오류 주입인데 exit 0")
	}
	if !strings.Contains(out.String(), "active_knowledge") || !strings.Contains(out.String(), "knowledge") {
		t.Errorf("기본 테이블 안내 문구 누락: %q", out.String())
	}
}

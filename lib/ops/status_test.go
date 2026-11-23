package ops

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/wire"
)

func vaultStatusFake(schemaVersion, lastScan string) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta WHERE key = 'schema_version'") {
			return [][]any{{schemaVersion}}, nil
		}
		if strings.Contains(sql, "FROM meta WHERE key = $1") && len(args) > 0 {
			if k, ok := args[0].(string); ok && k == "vault_last_scan" && lastScan != "" {
				return [][]any{{lastScan}}, nil
			}
			return nil, nil
		}
		return nil, nil
	}}
}

func runStatusJSON(t *testing.T, st wire.Store) (int, string, map[string]any) {
	t.Helper()
	statusSeams(t, func(context.Context, wire.Store, string) (string, error) { return "", nil })
	var out strings.Builder
	code := RunStatus(context.Background(), st, nil, strings.NewReader(""), &out)
	line := strings.TrimSpace(out.String())
	var m map[string]any
	if code == 0 {
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("출력 파싱: %v — %s", err, line)
		}
	}
	return code, line, m
}

func TestStatusVaultRowRoundTripOrder(t *testing.T) {
	payload := `{"at":"2026-11-23T01:02:03Z","files":5,"changed":2}`
	code, line, m := runStatusJSON(t, vaultStatusFake("16", payload))
	if code != 0 {
		t.Fatalf("exit=%d — %s", code, line)
	}
	if m["vault_indexed_at"] != "2026-11-23T01:02:03Z" {
		t.Fatalf("vault_indexed_at=%v", m["vault_indexed_at"])
	}
	if m["vault_files"].(float64) != 5 || m["vault_changed"].(float64) != 2 {
		t.Fatalf("vault files/changed=%v/%v, want 5/2", m["vault_files"], m["vault_changed"])
	}

	order := []string{"status", "schema_version", "pending", "hot_bytes_p95",
		"vault_indexed_at", "vault_files", "vault_changed"}
	last := -1
	for _, k := range order {
		quote := `"` + k + `":`
		idx := strings.Index(line, quote)
		if idx < 0 {
			t.Fatalf("출력에 키 %q 부재: %s", k, line)
		}
		if idx < last {
			t.Fatalf("키 %q이 순서 위반 (idx=%d < prev=%d): %s", k, idx, last, line)
		}
		last = idx
	}
}

func TestStatusVaultRowAbsentNull(t *testing.T) {
	code, line, _ := runStatusJSON(t, vaultStatusFake("16", ""))
	if code != 0 {
		t.Fatalf("exit=%d — %s", code, line)
	}
	if !strings.Contains(line, `"vault_indexed_at":null`) {
		t.Fatalf("vault_indexed_at null 부재: %s", line)
	}
	if !strings.Contains(line, `"vault_files":0`) || !strings.Contains(line, `"vault_changed":0`) {
		t.Fatalf("부재 시 카운트 0 아님: %s", line)
	}
}

func TestStatusVaultRowCorruptNull(t *testing.T) {
	code, line, _ := runStatusJSON(t, vaultStatusFake("16", "not-json"))
	if code != 0 {
		t.Fatalf("exit=%d — %s", code, line)
	}
	if !strings.Contains(line, `"vault_indexed_at":null`) {
		t.Fatalf("corrupt 시 null 아님: %s", line)
	}
}

func TestStatusEngineMemMB(t *testing.T) {
	t.Setenv("FLINT_DSN", "postgresql://flint:flint-dev@localhost:5434/flint")
	withComposePs(t, func(context.Context) ([]byte, error) {
		return []byte(`[{"Name":"flint-doltgres-1","State":"running","Publishers":[{"PublishedPort":5434}]}]`), nil
	})
	withEngineStats(t, func(context.Context) ([]byte, error) {
		return []byte("{\"Name\":\"flint-doltgres-1\",\"MemUsage\":\"50.5MiB / 3.84GiB\"}\n"), nil
	})
	code, line, m := runStatusJSON(t, vaultStatusFake("16", ""))
	if code != 0 {
		t.Fatalf("exit=%d — %s", code, line)
	}
	if m["engine_mem_mb"].(float64) != 50 {
		t.Fatalf("engine_mem_mb=%v, want 50", m["engine_mem_mb"])
	}

	prevTail := strings.Index(line, `"scan_suggest_p95_us":`)
	got := strings.Index(line, `"engine_mem_mb":`)
	if prevTail < 0 || got < 0 {
		t.Fatalf("키 부재(prev=%d, got=%d): %s", prevTail, got, line)
	}
	if got < prevTail {
		t.Fatalf("engine_mem_mb가 이전 tail(scan_suggest_p95_us)보다 앞: %s", line)
	}
}

func TestStatusEngineMemMBOmitted(t *testing.T) {
	t.Setenv("FLINT_DSN", "postgresql://flint:flint-dev@localhost:5434/flint")
	withComposePs(t, func(context.Context) ([]byte, error) {
		return []byte(`[{"Name":"flint-doltgres-1","State":"running","Publishers":[{"PublishedPort":5434}]}]`), nil
	})
	withEngineStats(t, func(context.Context) ([]byte, error) {
		return nil, errors.New("docker not installed")
	})
	code, line, _ := runStatusJSON(t, vaultStatusFake("16", ""))
	if code != 0 {
		t.Fatalf("exit=%d — %s", code, line)
	}
	if strings.Contains(line, "engine_mem_mb") {
		t.Fatalf("실패 시 engine_mem_mb 출력됨: %s", line)
	}
}

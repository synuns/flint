// status_test.go — v1.21.0 T3: the status vault row. The last index time,
// file count, and changed count ride on meta 'vault_last_scan' (JSON written
// by `flint index`); status appends vault_indexed_at/vault_files/
// vault_changed at the end of the existing key order (the statusV0180Output
// additive precedent — existing keys and their order unchanged). Absent or
// corrupt payloads degrade to null/0/0 (the metering-indication principle —
// it must not break status).
package ops

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/wire"
)

// vaultStatusFake — a fake answering meta schema_version and an optional
// meta vault_last_scan payload (the RunStatus meta lookups).
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

// runStatusJSON — one RunStatus call returning (exit code, the raw one-line
// output, the decoded object).
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

// TestStatusVaultRowRoundTripOrder — a stored vault_last_scan round-trips
// into the appended vault row, and the existing key order is untouched: the
// three vault keys come last, right after the previous tail (hot_bytes_p95).
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
	// key order: existing keys unchanged, the vault row appended at the end.
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

// TestStatusVaultRowAbsentNull — no vault_last_scan yet (never indexed):
// vault_indexed_at is null and the counts are 0 — status still exits 0.
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

// TestStatusVaultRowCorruptNull — a corrupt (non-JSON) payload degrades to
// null/0/0, never an error exit.
func TestStatusVaultRowCorruptNull(t *testing.T) {
	code, line, _ := runStatusJSON(t, vaultStatusFake("16", "not-json"))
	if code != 0 {
		t.Fatalf("exit=%d — %s", code, line)
	}
	if !strings.Contains(line, `"vault_indexed_at":null`) {
		t.Fatalf("corrupt 시 null 아님: %s", line)
	}
}

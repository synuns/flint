// meta_keys_v1194_test.go — the v1.19.4 R80-1 regression: doctor's meta-hygiene
// no longer warns its OWN gate stamps as unknown meta keys — last_events_prune_at
// (the db read-cleanup 24h gate, a pre-existing false positive) and the 2 ops-side
// stamps derived from meteringFreshnessGateKeys (single source, no duplicate list).
package ops

import (
	"context"
	"strings"
	"testing"
)

// TestMetaHygieneGateStampsKnown — a meta table holding exactly the 3 gate
// stamps (+1 fixed key) audits clean: status ok, no unknown-key warn (pre-fix:
// 3 unknown keys — doctor warning about its own stamps).
func TestMetaHygieneGateStampsKnown(t *testing.T) {
	// the doctor_test.go init pins the seam always-false — swap in the real
	// classification for this test (restored after).
	prevFn := KnownMetaKeyFn
	KnownMetaKeyFn = KnownMetaKey
	t.Cleanup(func() { KnownMetaKeyFn = prevFn })

	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		// the hygiene query only ("ORDER BY key") — the thresholds IN-list
		// query also contains "FROM meta" and must see no rows.
		if strings.Contains(sql, "ORDER BY key") {
			return [][]any{
				{"last_events_prune_at"},
				{"last_rollup_at"},
				{"last_threshold_snapshot_at"},
				{"schema_version"},
			}, nil
		}
		return nil, nil
	}}
	c := checkMetaHygiene(context.Background(), f)
	if c.Status != "ok" {
		t.Fatalf("게이트 스탬프 무경고 아님: status=%q detail=%q", c.Status, c.Detail)
	}
	if strings.Contains(c.Detail, "unknown") {
		t.Fatalf("unknown 경고 잔존: %s", c.Detail)
	}
	// the single-source derivation — every doctor metering gate key is known.
	for _, k := range meteringFreshnessGateKeys {
		if !KnownMetaKey(k) {
			t.Fatalf("meteringFreshnessGateKeys의 %q 미등록(단일 소스 파생 실패)", k)
		}
	}
	if !KnownMetaKey("last_events_prune_at") {
		t.Fatalf("last_events_prune_at 미등록(db 게이트 스탬프)")
	}
}

// TestMetaHygieneVaultKeysKnown — the v1.21.0 vault-bridge meta keys
// (vault_path·vault_last_scan from `flint index`, scan_stats from the A6
// scan p95 metering) audit clean — doctor must not warn its own surfaces
// (the R80-1 self-contradiction class). A genuinely unknown key still warns.
func TestMetaHygieneVaultKeysKnown(t *testing.T) {
	prevFn := KnownMetaKeyFn
	KnownMetaKeyFn = KnownMetaKey
	t.Cleanup(func() { KnownMetaKeyFn = prevFn })

	known := [][]any{
		{"schema_version"},
		{"vault_path"},
		{"vault_last_scan"},
		{"scan_stats"},
	}
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "ORDER BY key") {
			return known, nil
		}
		return nil, nil
	}}
	c := checkMetaHygiene(context.Background(), f)
	if c.Status != "ok" || strings.Contains(c.Detail, "unknown") {
		t.Fatalf("볼트 키 무경고 아님: status=%q detail=%q", c.Status, c.Detail)
	}
	for _, k := range []string{"vault_path", "vault_last_scan", "scan_stats"} {
		if !KnownMetaKey(k) {
			t.Fatalf("%q 미등록(v1.21.0 볼트/A6 메타 키)", k)
		}
	}

	// a genuinely unknown key still warns (the hygiene purpose is intact).
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "ORDER BY key") {
			return append(known, []any{"mystery_deprecated_key"}), nil
		}
		return nil, nil
	}
	c = checkMetaHygiene(context.Background(), f)
	if c.Status == "ok" || !strings.Contains(c.Detail, "mystery_deprecated_key") {
		t.Fatalf("진짜 unknown 키 경고 안 함: status=%q detail=%q", c.Status, c.Detail)
	}
}

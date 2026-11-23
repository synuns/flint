package ops

import (
	"context"
	"strings"
	"testing"
)

func TestMetaHygieneGateStampsKnown(t *testing.T) {

	prevFn := KnownMetaKeyFn
	KnownMetaKeyFn = KnownMetaKey
	t.Cleanup(func() { KnownMetaKeyFn = prevFn })

	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {

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

	for _, k := range meteringFreshnessGateKeys {
		if !KnownMetaKey(k) {
			t.Fatalf("meteringFreshnessGateKeys의 %q 미등록(단일 소스 파생 실패)", k)
		}
	}
	if !KnownMetaKey("last_events_prune_at") {
		t.Fatalf("last_events_prune_at 미등록(db 게이트 스탬프)")
	}
}

func TestMetaHygieneAutoKeysKnown(t *testing.T) {
	prevFn := KnownMetaKeyFn
	KnownMetaKeyFn = KnownMetaKey
	t.Cleanup(func() { KnownMetaKeyFn = prevFn })

	known := [][]any{
		{"schema_version"},
		{"auto_mode"},
		{"last_shadow_at"},
		{"auto_streak_suggest_min_score"},
		{"auto_rounds_suggest_min_score"},
	}
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "ORDER BY key") {
			return known, nil
		}
		return nil, nil
	}}
	c := checkMetaHygiene(context.Background(), f)
	if c.Status != "ok" || strings.Contains(c.Detail, "unknown") {
		t.Fatalf("auto 키 무경고 아님: status=%q detail=%q", c.Status, c.Detail)
	}
	for _, k := range []string{"auto_mode", "last_shadow_at", "auto_streak_suggest_min_score", "auto_rounds_suggest_min_score"} {
		if !KnownMetaKey(k) {
			t.Fatalf("%q 미등록(v1.23.0/v1.23.1 auto 튜닝 메타 키)", k)
		}
	}

	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "ORDER BY key") {
			return append(known, []any{"auto_bogus_key"}), nil
		}
		return nil, nil
	}
	c = checkMetaHygiene(context.Background(), f)
	if c.Status == "ok" || !strings.Contains(c.Detail, "auto_bogus_key") {
		t.Fatalf("auto_* 임의 키 경고 안 함: status=%q detail=%q", c.Status, c.Detail)
	}
}

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

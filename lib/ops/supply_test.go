// supply_test.go — behavioral invariants of the ops-moved files (report,
// report_html, backup, sigverify, rules_sync). The report effect-metric computation
// unchanged, backup's 3-layer defense (0600 enforcement, sidecar, rotation), restore
// guards (checksum, sidecar, masking), the sigverify embedded-key contract, and the
// dumpStore delegation (the realstore_supply path) — all guaranteed by the ops package alone (fakeStore — the doctor_test convention).
package ops

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/backup"
)

// supplyFake — a minimal fake for the report query paths (out-of-window key exclusion,
// corrupt skip, etc. — a minimal version of the existing report_test.go conventions). Only daily has values; the other tallies are zero behavior.
func supplyFake(daily map[string]string) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "daily:"):
			rows := [][]any{}
			for k, v := range daily {
				rows = append(rows, []any{k, v})
			}
			return rows, nil
		case strings.Contains(sql, "read_stats"):
			return [][]any{{"read_stats", `{"calls":0}`}, {"hot_stats", `{"lens":[]}`}}, nil
		case strings.Contains(sql, "invalid_at IS NOT NULL"):
			return [][]any{{int64(0), int64(0), int64(0)}}, nil
		}
		return nil, nil
	}}
}

// TestSupplyReportEffectUnchanged — the report effect-metric computation unchanged:
// the daily-sum single derivation chain (zero_pct, hit_pct, near_pct, dup_prevented,
// dup_pct) computes the same as before the move (floored integer %; zero denominator 0).
func TestSupplyReportEffectUnchanged(t *testing.T) {
	now := time.Now().UTC()
	d := func(daysAgo int) string { return now.AddDate(0, 0, -daysAgo).Format("2006-01-02") }
	// 2-day window: daily read 10, zero 2, near 1, write 10, dup 5 (both days identical).
	row := `{"write":10,"dup":5,"read":10,"read_zero":2,"read_zero_near":1}`
	f := supplyFake(map[string]string{"daily:" + d(0): row, "daily:" + d(1): row})
	var out strings.Builder
	code := RunReport(context.Background(), f, []string{"--days", "2"}, nil, &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	var m struct {
		Days    []map[string]any `json:"days"`
		ZeroPct int              `json:"zero_pct"`
		DupPct  int              `json:"dup_pct"`
		Effect  struct {
			HitPct       int    `json:"hit_pct"`
			NearPct      int    `json:"near_pct"`
			DupPrevented int    `json:"dup_prevented"`
			FallbackPct  int    `json:"fallback_pct"`
			ProfileHint  string `json:"profile_hint"`
		} `json:"effect"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &m); err != nil {
		t.Fatalf("JSON 아님: %v (%s)", err, out.String())
	}
	if len(m.Days) != 2 {
		t.Fatalf("days 길이=%d, want 2", len(m.Days))
	}
	if m.ZeroPct != 20 { // 4/20
		t.Errorf("zero_pct=%d, want 20", m.ZeroPct)
	}
	if m.DupPct != 33 { // 10/(20+10)
		t.Errorf("dup_pct=%d, want 33", m.DupPct)
	}
	if m.Effect.HitPct != 80 || m.Effect.NearPct != 10 || m.Effect.DupPrevented != 10 {
		t.Errorf("effect hit/near/dup = %d/%d/%d, want 80/10/10",
			m.Effect.HitPct, m.Effect.NearPct, m.Effect.DupPrevented)
	}
	if m.Effect.FallbackPct != 0 {
		t.Errorf("fallback_pct=%d, want 0(이벤트 없음)", m.Effect.FallbackPct)
	}
	if !strings.Contains(m.Effect.ProfileHint, "insufficient sample") {
		t.Errorf("profile_hint=%q, want 표본 부족 보류", m.Effect.ProfileHint)
	}
}

// withBackupSeams — swaps the DumpFn/RestoreFn/RotateFn/KeepFn seams (restored after the test).
func withBackupSeams(t *testing.T, dump string, dumpErr error, restored *string) {
	t.Helper()
	prevDump, prevRestore, prevRotate, prevKeep := DumpFn, RestoreFn, RotateFn, KeepFn
	DumpFn = func(ctx context.Context, dsn string, w io.Writer) error {
		if dumpErr != nil {
			return dumpErr
		}
		_, err := io.WriteString(w, dump)
		return err
	}
	RestoreFn = func(ctx context.Context, dsn string, r io.Reader) error {
		b, _ := io.ReadAll(r)
		if restored != nil {
			*restored = string(b)
		}
		return nil
	}
	RotateFn = func(dir string, keep int) ([]string, error) { return nil, nil }
	KeepFn = func(getKV func(string) (string, error)) int { return 14 }
	t.Cleanup(func() { DumpFn, RestoreFn, RotateFn, KeepFn = prevDump, prevRestore, prevRotate, prevKeep })
}

// TestSupplyBackupDefenseLayers — backup's 3-layer defense: (1) 0600 enforcement for
// sensitive dumps, (2) the original + .sha256 sidecar (ADR-0006), (3) a valid one-line JSON success output (+ the rotation summary).
func TestSupplyBackupDefenseLayers(t *testing.T) {
	withBackupSeams(t, "-- flint dump\nBEGIN;\nCOMMIT;\n", nil, nil)
	path := filepath.Join(t.TempDir(), "b.sql")
	var out strings.Builder
	code := RunBackup(context.Background(), &fakeStore{}, []string{"--out", path}, nil, &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("파일 미생성: %v", err)
	}
	if fi.Mode().Perm() != 0o600 { // (1) plaintext dump permission enforcement
		t.Errorf("퍼모드=%v, want 0600", fi.Mode().Perm())
	}
	side, rerr := os.ReadFile(path + ".sha256") // (2) sidecar
	if rerr != nil {
		t.Fatalf("사이드카 미생성: %v", rerr)
	}
	h := sha256.Sum256([]byte("-- flint dump\nBEGIN;\nCOMMIT;\n"))
	if !strings.HasPrefix(string(side), hex.EncodeToString(h[:])) {
		t.Errorf("사이드카 체크섬 불일치: %q", side)
	}
	var m map[string]any // (3) the one-line JSON contract
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &m); err != nil {
		t.Fatalf("성공 출력이 JSON 아님: %v (%s)", err, out.String())
	}
	if m["status"] != "ok" || m["sha256"] == nil {
		t.Errorf("성공 JSON=%v — status/sha256 계약 위반", m)
	}
}

// TestSupplyBackupOutDashStdout — `--out -` follows the POSIX convention (path "-" =
// stdout), fully identical to omitting --out: the dump body to stdout, the plaintext
// warning kept, and no `-` or `-.sha256` files created in cwd.
func TestSupplyBackupOutDashStdout(t *testing.T) {
	t.Chdir(t.TempDir())
	dump := "-- flint dump\nBEGIN;\nCOMMIT;\n"
	withBackupSeams(t, dump, nil, nil)
	var out strings.Builder
	code := RunBackup(context.Background(), &fakeStore{}, []string{"--out", "-"}, nil, &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	if got, want := out.String(), dump; got != want {
		t.Fatalf("stdout=%q, want %q", got, want)
	}
	for _, name := range []string{"-", "-.sha256"} {
		if _, err := os.Stat(name); err == nil {
			t.Errorf("`--out -`는 파일 %q을(를) 만들면 안 된다", name)
		}
	}
}

// TestSupplyRestoreGuards — restore guards: a checksum mismatch or missing sidecar is
// blocked before replay (RestoreFn not called); a masked dump is refused for restore.
func TestSupplyRestoreGuards(t *testing.T) {
	dir := t.TempDir()
	plain := []byte("BEGIN;\nCOMMIT;\n")

	// (1) checksum mismatch — blocking arbitrary SQL execution.
	p1 := filepath.Join(dir, "mismatch.sql")
	os.WriteFile(p1, plain, 0o600)
	os.WriteFile(p1+".sha256", []byte("0000000000000000000000000000000000000000000000000000000000000000  mismatch.sql\n"), 0o600)
	// (2) missing sidecar — blocking unverified replay.
	p2 := filepath.Join(dir, "noside.sql")
	os.WriteFile(p2, plain, 0o600)
	// (3) masked dump — diagnostics-only, restore refused (the sidecar is valid).
	p3 := filepath.Join(dir, "masked.sql")
	masked := []byte(backup.MaskDumpHeader + "\n-- masked\n")
	os.WriteFile(p3, masked, 0o600)
	mh := sha256.Sum256(masked)
	backup.WriteSidecar(p3, hex.EncodeToString(mh[:]))

	for _, tc := range []struct {
		name, path, want string
	}{
		{"mismatch", p1, "checksum_mismatch"},
		{"nosidecar", p2, "sidecar"},
		{"masked", p3, "diagnostics and sharing"},
	} {
		restored := ""
		withBackupSeams(t, "", nil, &restored)
		var out strings.Builder
		code := RunRestore(context.Background(), &fakeStore{}, []string{"--file", tc.path}, nil, &out)
		if code != 1 {
			t.Errorf("%s: exit=%d, want 1 (%s)", tc.name, code, out.String())
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Errorf("%s: 출력에 %q 없음: %s", tc.name, tc.want, out.String())
		}
		if restored != "" {
			t.Errorf("%s: 차단됐는데 재생 실행: %q", tc.name, restored)
		}
	}
}

// TestSupplySigVerifyEmbeddedKey — the sigverify embedded-key contract: a verifier
// injected with PublicPEM's scheme branching, tamper rejection, and the unconfigured transition mode.
func TestSupplySigVerifyEmbeddedKey(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	data := []byte("checksums payload")
	v := ReleaseVerifier{PublicPEM: pemBytes}
	if !v.Configured() {
		t.Fatal("Configured=false")
	}
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data))
	if err := v.Verify(data, sig); err != nil {
		t.Fatalf("ed25519 검증: %v", err)
	}
	if err := v.Verify([]byte("tampered"), sig); err == nil {
		t.Fatal("변조 데이터 통과")
	}
	bad := ReleaseVerifier{} // unconfigured transition mode — the path passes, verification refuses
	if bad.Configured() {
		t.Fatal("빈 키인데 Configured")
	}
	if err := bad.Verify(data, sig); err == nil {
		t.Fatal("미구성인데 검증 통과")
	}
}

// dumpDelegFake — the dumpStore delegation-path fake: has the Dump method (the same
// contract as the realStore delegation in realstore_supply.go).
type dumpDelegFake struct {
	*fakeStore
	dump   string
	called bool
}

func (d *dumpDelegFake) Dump(ctx context.Context, w io.Writer) error {
	d.called = true
	_, err := io.WriteString(w, d.dump)
	return err
}

// TestSupplyDumpDelegation — when the injected st implements Dump, the delegation
// path takes precedence over the dumpFn seam fallback (owned by realstore_supply).
func TestSupplyDumpDelegation(t *testing.T) {
	withBackupSeams(t, "SEAM-FALLBACK-NEVER", nil, nil)
	st := &dumpDelegFake{fakeStore: &fakeStore{}, dump: "DELEGATED-DUMP\n"}
	path := filepath.Join(t.TempDir(), "d.sql")
	var out strings.Builder
	code := RunBackup(context.Background(), st, []string{"--out", path}, nil, &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	if !st.called {
		t.Fatal("Dump 위임 미호출 — seam 폴백으로 새어나감")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "DELEGATED-DUMP\n" {
		t.Fatalf("덤프 원문=%q, want 위임 분", b)
	}
}

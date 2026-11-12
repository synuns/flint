// supply_test.go — v0.40.0 T3 Review Focus: ops 3탄 이동(report·report_html·
// backup·sigverify·rules_sync)의 행위 불변. report 효과 지표 산출 불변·
// backup 3층 방어(0600 강제·사이드카·로테이션)·restore 가드(체크섬·사이드카·
// 마스킹)·sigverify 임베드 키 계약·dumpStore 위임(realstore_supply 경로)을
// ops 패키지 단독(fakeStore — doctor_test 관례)으로 담보한다.
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

// supplyFake — report 조회 전 경로 fake(창 밖 키 배제·파손 스킵 등 기존
// report_test.go 관례의 최소판). daily만 값, 나머지 집계는 영(零) 동작.
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

// TestSupplyReportEffectUnchanged — report 효과 지표 산출 불변: daily 합산
// 단일 유도 사슬(zero_pct·hit_pct·near_pct·dup_prevented·dup_pct)이 이동
// 전과 동일하게 산출된다(버림 정수%·제로 분모 0).
func TestSupplyReportEffectUnchanged(t *testing.T) {
	now := time.Now().UTC()
	d := func(daysAgo int) string { return now.AddDate(0, 0, -daysAgo).Format("2006-01-02") }
	// 2일 창: 일별 read 10·zero 2·near 1·write 10·dup 5 (2일 동일).
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
	if !strings.Contains(m.Effect.ProfileHint, "보류") {
		t.Errorf("profile_hint=%q, want 표본 부족 보류", m.Effect.ProfileHint)
	}
}

// withBackupSeams — DumpFn·RestoreFn·RotateFn·KeepFn seam 교체(테스트 후 원복).
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

// TestSupplyBackupDefenseLayers — backup 3층 방어: ① 민감 덤프 0600 강제
// ② 원본+.sha256 사이드카(ADR-0006) ③ 성공 출력은 유효 한 줄 JSON(+로테이션 요약).
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
	if fi.Mode().Perm() != 0o600 { // ① 평문 덤프 퍼모드 강제
		t.Errorf("퍼모드=%v, want 0600", fi.Mode().Perm())
	}
	side, rerr := os.ReadFile(path + ".sha256") // ② 사이드카
	if rerr != nil {
		t.Fatalf("사이드카 미생성: %v", rerr)
	}
	h := sha256.Sum256([]byte("-- flint dump\nBEGIN;\nCOMMIT;\n"))
	if !strings.HasPrefix(string(side), hex.EncodeToString(h[:])) {
		t.Errorf("사이드카 체크섬 불일치: %q", side)
	}
	var m map[string]any // ③ 한 줄 JSON 계약
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &m); err != nil {
		t.Fatalf("성공 출력이 JSON 아님: %v (%s)", err, out.String())
	}
	if m["status"] != "ok" || m["sha256"] == nil {
		t.Errorf("성공 JSON=%v — status/sha256 계약 위반", m)
	}
}

// TestSupplyRestoreGuards — restore 가드: 체크섬 불일치·사이드카 부재는
// 재생 전 차단(RestoreFn 미호출), 마스킹 덤프는 복원 거부.
func TestSupplyRestoreGuards(t *testing.T) {
	dir := t.TempDir()
	plain := []byte("BEGIN;\nCOMMIT;\n")

	// ① 체크섬 불일치 — 임의 SQL 실행 차단.
	p1 := filepath.Join(dir, "mismatch.sql")
	os.WriteFile(p1, plain, 0o600)
	os.WriteFile(p1+".sha256", []byte("0000000000000000000000000000000000000000000000000000000000000000  mismatch.sql\n"), 0o600)
	// ② 사이드카 부재 — 무검증 재생 차단.
	p2 := filepath.Join(dir, "noside.sql")
	os.WriteFile(p2, plain, 0o600)
	// ③ 마스킹 덤프 — 진단 전용, 복원 거부(사이드카는 유효).
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
		{"masked", p3, "진단·공유 전용"},
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

// TestSupplySigVerifyEmbeddedKey — sigverify 임베드 키 계약: PublicPEM
// 주입 검증기의 스킴 분기·변조 거부·미구성 전환 모드.
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
	bad := ReleaseVerifier{} // 미구성 전환 모드 — 경로 통과·검증 거부
	if bad.Configured() {
		t.Fatal("빈 키인데 Configured")
	}
	if err := bad.Verify(data, sig); err == nil {
		t.Fatal("미구성인데 검증 통과")
	}
}

// dumpDelegFake — dumpStore 위임 경로 fake: Dump 메서드 보유(realStore
// realstore_supply.go 위임과 동일 계약).
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

// TestSupplyDumpDelegation — v0.34.3 R14: 주입 st가 Dump를 구현하면
// dumpFn seam 폴백이 아니라 위임 경로가 우선한다(realstore_supply 소유).
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

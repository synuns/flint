// update_test.go — auto-update contracts: version comparison, checksums, extraction,
// the full flow (httptest injection), and the update-notice cache.
package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sha256Hex(b []byte) string {
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:])
}

// withFakeVersion — injects the version package vars (the test binary is "dev"
// without ldflags injection).
func withFakeVersion(t *testing.T, v string) {
	t.Helper()
	prev := version
	version = v
	t.Cleanup(func() { version = prev })
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.22.0", "0.21.2", 1},
		{"0.21.2", "0.22.0", -1},
		{"1.0.0", "0.99.99", 1},
		{"0.10.0", "0.9.9", 1},
		{"0.21.2", "0.21.2", 0},
	}
	for _, tc := range cases {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compare(%s,%s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	if validVersion("dev") || validVersion("0.21") || !validVersion("0.21.2") {
		t.Error("validVersion 판별 오류")
	}
}

func TestChecksumFor(t *testing.T) {
	sums := "abc0  flint_0.22.0_linux_amd64.tar.gz\ndef0 *flint_0.22.0_darwin_arm64.tar.gz\n"
	if got, _ := checksumFor(sums, "flint_0.22.0_darwin_arm64.tar.gz"); got != "def0" {
		t.Errorf("별표 항목 추출 실패: %q", got)
	}
	if _, err := checksumFor(sums, "missing.tar.gz"); err == nil {
		t.Error("missing entry should error")
	}
}

// buildTarGz — a tar.gz fixture containing a single flint binary.
func buildTarGz(t *testing.T, bin []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "flint", Mode: 0o755, Size: int64(len(bin)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(bin); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestExtractBinary(t *testing.T) {
	if _, err := extractBinary([]byte("not gzip")); err == nil {
		t.Error("무효 아카이브 에러 아님")
	}
	got, err := extractBinary(buildTarGz(t, []byte("payload")))
	if err != nil || string(got) != "payload" {
		t.Errorf("추출 = %q %v", got, err)
	}
}

// TestApplyUpdateRejectsBadSignature — signature-gate wiring —
// with the public key embedded, a .sig mismatch is a hard reject and a missing .sig is rejected too
// (fail-closed). Transition mode (empty key) passes.
func TestApplyUpdateRejectsBadSignature(t *testing.T) {
	newBin := []byte("#!/bin/sh\necho '{\"version\":\"9.9.9\"}'\n")
	// includes verifier behavior with real key fixtures (mismatch induced: a stranger's key).
	pub, err := os.ReadFile("testdata/cosign-ecdsa.pub")
	if err != nil {
		t.Fatal(err)
	}
	prevPub := releasePubPEM
	t.Cleanup(func() { releasePubPEM = prevPub; updateSkipSignature = false })
	updateSkipSignature = false

	// (a) .sig present + public key embedded + garbage signature -> rejected.
	releasePubPEM = pub
	updateTestSigAsset = "bm90LWEtc2ln" // base64-decodes but the signature mismatches
	t.Cleanup(func() { updateTestSigAsset = "" })
	withUpdateServer(t, "9.9.9", newBin)
	if _, err := applyUpdate("9.9.9"); err == nil || !strings.Contains(err.Error(), "refus") {
		t.Fatalf("garbage signature = %v, want refused", err)
	}
	// (a-2) same state + --insecure-skip-signature -> passes.
	updateSkipSignature = true
	withUpdateServer(t, "9.9.8", newBin)
	_, err2 := applyUpdate("9.9.8")
	updateSkipSignature = false
	if err2 != nil {
		t.Fatalf("우회 플래그 통과 실패: %v", err2)
	}
	updateTestSigAsset = ""

	// (b) .sig missing + Configured -> rejected.
	releasePubPEM = pub
	withUpdateServer(t, "9.8.0", newBin)
	if _, err := applyUpdate("9.8.0"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf(".sig missing = %v, want refused", err)
	}

	// (c) transition mode (empty public key) + no .sig -> passes (existing compatibility).
	releasePubPEM = nil
	withUpdateServer(t, "9.7.0", newBin)
	if _, err := applyUpdate("9.7.0"); err != nil {
		t.Fatalf("전환 모드 통과 실패: %v", err)
	}
}

// updateTestSigAsset — injects a served .sig asset (empty = not provided). The server handler
// reflects it in both the listing and the content.
var updateTestSigAsset string

// newUpdateServer — a fake releases API + asset server. newBin is the new binary's
// content (must be a shell script printing the version JSON when run, to pass verification).
func newUpdateServer(t *testing.T, latest string, newBin []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewTLSServer(mux)
	// client factory injection — srv.Client() trusts the self-signed cert (no
	// InsecureSkipVerify in production binaries).
	prevClient := updateClientFn
	updateClientFn = func(timeout time.Duration) *http.Client {
		c := srv.Client()
		c.Timeout = timeout
		return c
	}
	t.Cleanup(func() { updateClientFn = prevClient })
	t.Cleanup(srv.Close)
	assetName := fmt.Sprintf("flint_%s_%s_%s.tar.gz", latest, runtime.GOOS, runtime.GOARCH)
	archive := buildTarGz(t, newBin)
	mux.HandleFunc("/repos/synuns/flint/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		// when updateTestSigAsset (test injection) is non-empty, the .sig asset is
		// included in the listing and served too (for verifying the signature-gate wiring).
		sigAsset := ""
		if updateTestSigAsset != "" {
			sigAsset = fmt.Sprintf(`,{"name":"checksums.txt.sig","browser_download_url":%q}`, srv.URL+"/dl/checksums.txt.sig")
			mux.HandleFunc("/dl/checksums.txt.sig", func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, updateTestSigAsset)
			})
		}
		fmt.Fprintf(w, `{"tag_name":"v%s","assets":[{"name":%q,"browser_download_url":%q},{"name":"checksums.txt","browser_download_url":%q}%s]}`,
			latest, assetName, srv.URL+"/dl/"+assetName, srv.URL+"/dl/checksums.txt", sigAsset)
	})
	mux.HandleFunc("/dl/"+assetName, func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	})
	// sha256 is over the archive bytes — a closure so it can be computed inside the handler.
	sum := sha256Hex(archive)
	mux.HandleFunc("/dl/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", sum, assetName)
	})
	return srv
}

// withUpdateServer — the common API-base injection procedure.
func withUpdateServer(t *testing.T, latest string, newBin []byte) {
	t.Helper()
	srv := newUpdateServer(t, latest, newBin)
	prev := updateAPIBase
	updateAPIBase = srv.URL
	t.Cleanup(func() { updateAPIBase = prev })
}

// TestUpdateFlowReplacesBinary — the full flow: check -> download -> verify -> replace.
// Injects a temp 'current binary' via executablePathFn to verify a real file
// replacement (the new binary that passes verification is a shell script printing version).
func TestUpdateFlowReplacesBinary(t *testing.T) {
	// the real public key is embedded, so default hard verification is on — this test
	// verifies the replacement flow, so it pins transition mode (empty key).
	prevPub := releasePubPEM
	releasePubPEM = nil
	t.Cleanup(func() { releasePubPEM = prevPub })
	withFakeVersion(t, "0.1.0")
	dir := t.TempDir()
	self := filepath.Join(dir, "flint")
	if err := os.WriteFile(self, []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	prevExe := executablePathFn
	executablePathFn = func() (string, error) { return self, nil }
	t.Cleanup(func() { executablePathFn = prevExe })

	newBin := []byte("#!/bin/sh\necho '{\"version\":\"9.9.9\"}'\n")
	withUpdateServer(t, "9.9.9", newBin)

	if _, err := applyUpdate("9.9.9"); err != nil {
		t.Fatalf("applyUpdate: %v", err)
	}
	got, err := os.ReadFile(self)
	if err != nil || !bytes.Contains(got, []byte("9.9.9")) {
		t.Fatalf("교체 결과 = %q %v — 원본이 안 바뀜", got, err)
	}
	fi, _ := os.Stat(self)
	if fi.Mode()&0o111 == 0 {
		t.Error("실행 비트 유실")
	}
}

// TestUpdateFlowChecksumMismatch — on checksum mismatch the original is untouched (immediate abort).
// A single TLS server serves metadata, assets (valid), and the poisoned checksum together.
func TestUpdateFlowChecksumMismatch(t *testing.T) {
	// the real public key is embedded, so default hard verification is on — this test
	// verifies the replacement flow, so it pins transition mode (empty key).
	prevPub := releasePubPEM
	releasePubPEM = nil
	t.Cleanup(func() { releasePubPEM = prevPub })
	dir := t.TempDir()
	self := filepath.Join(dir, "flint")
	if err := os.WriteFile(self, []byte("original"), 0o755); err != nil {
		t.Fatal(err)
	}
	prevExe := executablePathFn
	executablePathFn = func() (string, error) { return self, nil }
	t.Cleanup(func() { executablePathFn = prevExe })

	newBin := []byte("#!/bin/sh\necho '{\"version\":\"9.9.9\"}'\n")
	archive := buildTarGz(t, newBin)
	assetName := fmt.Sprintf("flint_9.9.9_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	mux := http.NewServeMux()
	srv := httptest.NewTLSServer(mux) // create first — the URL must be known
	t.Cleanup(srv.Close)
	mux.HandleFunc("/repos/synuns/flint/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v9.9.9","assets":[{"name":%q,"browser_download_url":%q},{"name":"checksums.txt","browser_download_url":%q}]}`,
			assetName, srv.URL+"/dl/"+assetName, srv.URL+"/bad")
	})
	mux.HandleFunc("/dl/"+assetName, func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	})
	mux.HandleFunc("/bad", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "0000000000000000000000000000000000000000000000000000000000000000  "+assetName+"\n")
	})
	prevClient := updateClientFn
	updateClientFn = func(timeout time.Duration) *http.Client {
		c := srv.Client()
		c.Timeout = timeout
		return c
	}
	t.Cleanup(func() { updateClientFn = prevClient })
	prev := updateAPIBase
	updateAPIBase = srv.URL
	t.Cleanup(func() { updateAPIBase = prev })

	if _, err := applyUpdate("9.9.9"); err == nil {
		t.Fatal("체크섬 불일치인데 성공")
	}
	got, _ := os.ReadFile(self)
	if string(got) != "original" {
		t.Fatalf("불일치 경로에서 원본 훼손: %q", got)
	}
}

// TestMaybeUpdateNoticeCache — within the cache TTL the check is decided without network
// (a cache equal to the running version is self-consistent — no contradiction refetch);
// on expiry it re-checks and refreshes the cache.
func TestMaybeUpdateNoticeCache(t *testing.T) {
	withFakeVersion(t, "0.1.0")
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	// fresh cache: latest == current -> self-consistent, no output, no network call.
	c := updateCache{Latest: "0.1.0", CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	b, _ := json.Marshal(c)
	os.WriteFile(updateCacheFile(), b, 0o600)
	// live server — within the TTL there must be no call at all.
	withUpdateServer(t, "9.9.9", nil)
	if got := maybeUpdateNotice(); got != "" {
		t.Errorf("캐시 무시·무출력 위반: %q", got)
	}
	// expired cache + latest 9.9.9 -> one notice line + cache refresh.
	c.CheckedAt = time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	b, _ = json.Marshal(c)
	os.WriteFile(updateCacheFile(), b, 0o600)
	if got := maybeUpdateNotice(); !strings.Contains(got, "flint update") {
		t.Errorf("안내 없음: %q", got)
	}
	var after updateCache
	rb, _ := os.ReadFile(updateCacheFile())
	_ = json.Unmarshal(rb, &after)
	if after.Latest != "9.9.9" {
		t.Errorf("캐시 갱신 실패: %+v", after)
	}
}

// TestDownloadRejectsNonHTTPS — plaintext-scheme URLs are rejected for
// download (defense against response tampering and downgrade).
func TestDownloadRejectsNonHTTPS(t *testing.T) {
	if _, err := download("http://example.com/asset"); err == nil {
		t.Error("http:// URL 거부 안 됨")
	}
}

// --- v1.26.0 T1 cache 3 principles: ETag round trip, contradiction detector,
// old-cache compat, silent network failure, checked_at in noticeLine. ---

// newETagServer — a fake releases API honoring If-None-Match (304 on exact match)
// and counting requests. An empty etag never matches, so it always serves 200.
func newETagServer(t *testing.T, latest, etag string) *int32 {
	t.Helper()
	var hits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/synuns/flint/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if etag != "" && r.Header.Get("If-None-Match") == etag {
			w.Header().Set("ETag", etag)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		fmt.Fprintf(w, `{"tag_name":"v%s","assets":[]}`, latest)
	})
	srv := httptest.NewTLSServer(mux)
	prevClient := updateClientFn
	updateClientFn = func(timeout time.Duration) *http.Client {
		c := srv.Client()
		c.Timeout = timeout
		return c
	}
	prev := updateAPIBase
	updateAPIBase = srv.URL
	t.Cleanup(func() { updateAPIBase = prev; updateClientFn = prevClient })
	t.Cleanup(srv.Close)
	return &hits
}

func writeUpdateCache(t *testing.T, c updateCache) {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(updateCacheFile(), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readUpdateCache(t *testing.T) updateCache {
	t.Helper()
	var c updateCache
	b, err := os.ReadFile(updateCacheFile())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// TestUpdateCacheETagRoundTrip — expired cache + ETag: the refresh sends
// If-None-Match, the 304 keeps the cached latest, and checked_at is refreshed
// to now. A follow-up call within the TTL makes no further request.
func TestUpdateCacheETagRoundTrip(t *testing.T) {
	withFakeVersion(t, "0.1.0")
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	const etag = `"deadbeef"`
	hits := newETagServer(t, "9.9.9", etag)
	writeUpdateCache(t, updateCache{
		Latest:    "9.9.9",
		ETag:      etag,
		CheckedAt: time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339),
	})
	if got := maybeUpdateNotice(); !strings.Contains(got, "flint update") {
		t.Fatalf("304 경로 안내 없음: %q", got)
	}
	if n := atomic.LoadInt32(hits); n != 1 {
		t.Fatalf("요청 수 = %d, want 1 (조건부 1회)", n)
	}
	after := readUpdateCache(t)
	if after.Latest != "9.9.9" || after.ETag != etag {
		t.Errorf("304 캐시 승계 실패: %+v", after)
	}
	checked, err := time.Parse(time.RFC3339, after.CheckedAt)
	if err != nil || time.Since(checked) > time.Minute {
		t.Errorf("checked_at 미갱신: %q %v", after.CheckedAt, err)
	}
	// within the refreshed TTL there must be no further request.
	maybeUpdateNotice()
	if n := atomic.LoadInt32(hits); n != 1 {
		t.Errorf("TTL 내 재요청: %d", n)
	}
}

// TestFetchLatestVersionRefreshesCache — v1.26.1: an explicit check
// (`flint update [--check]`, doctor --check update) refreshes the update-check
// cache — latest, ETag, and checked_at=now — so the doctor Fix guidance
// "flint doctor --check update (refresh the version cache)" is true. The
// refreshed cache then satisfies the session-start notice without another
// network call.
func TestFetchLatestVersionRefreshesCache(t *testing.T) {
	withFakeVersion(t, "0.1.0")
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	const etag = `"feedface"`
	hits := newETagServer(t, "9.9.7", etag)
	writeUpdateCache(t, updateCache{
		Latest:    "9.9.6",
		ETag:      `"stale"`,
		CheckedAt: time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339),
	})
	latest, err := fetchLatestVersion()
	if err != nil {
		t.Fatalf("fetchLatestVersion: %v", err)
	}
	if latest != "9.9.7" {
		t.Fatalf("latest=%q, want 9.9.7", latest)
	}
	after := readUpdateCache(t)
	if after.Latest != "9.9.7" || after.ETag != etag {
		t.Errorf("명시적 검사가 캐시를 갱신하지 않음: %+v", after)
	}
	checked, err := time.Parse(time.RFC3339, after.CheckedAt)
	if err != nil || time.Since(checked) > time.Minute {
		t.Errorf("checked_at 미갱신: %q %v", after.CheckedAt, err)
	}
	// the refreshed cache serves the notice path with no further request.
	if got := maybeUpdateNotice(); !strings.Contains(got, "flint update") {
		t.Fatalf("갱신된 캐시로 안내 없음: %q", got)
	}
	if n := atomic.LoadInt32(hits); n != 1 {
		t.Errorf("요청 수 = %d, want 1 (캐시 히트)", n)
	}
}

// TestMaybeUpdateNoticeContradictionRefetch — a fresh cache whose latest is
// OLDER than the running version is a contradiction: the cache is ignored and
// an immediate re-fetch happens (here the server's 9.9.9 yields a notice).
func TestMaybeUpdateNoticeContradictionRefetch(t *testing.T) {
	withFakeVersion(t, "0.1.0")
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	hits := newETagServer(t, "9.9.9", "")
	writeUpdateCache(t, updateCache{Latest: "0.0.1", CheckedAt: time.Now().UTC().Format(time.RFC3339)})
	if got := maybeUpdateNotice(); !strings.Contains(got, "flint update") {
		t.Fatalf("모순 캐시 재검사 안내 없음: %q", got)
	}
	if n := atomic.LoadInt32(hits); n != 1 {
		t.Errorf("모순 검출기 재 fetch 없음: %d", n)
	}
	if after := readUpdateCache(t); after.Latest != "9.9.9" {
		t.Errorf("모순 경로 캐시 갱신 실패: %+v", after)
	}
}

// TestMaybeUpdateNoticeEqualNoFetch — a fresh cache equal to the running
// version is self-consistent: no re-fetch, no notice.
func TestMaybeUpdateNoticeEqualNoFetch(t *testing.T) {
	withFakeVersion(t, "0.1.0")
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	hits := newETagServer(t, "9.9.9", "")
	writeUpdateCache(t, updateCache{Latest: "0.1.0", CheckedAt: time.Now().UTC().Format(time.RFC3339)})
	if got := maybeUpdateNotice(); got != "" {
		t.Errorf("등호 캐시 출력 위반: %q", got)
	}
	if n := atomic.LoadInt32(hits); n != 0 {
		t.Errorf("등호 캐시에서 fetch 발생: %d", n)
	}
}

// TestMaybeUpdateNoticeOldCacheFullFetch — a cache written before the ETag
// field existed (etag absent = "") gets a full fetch: no If-None-Match sent,
// and the refreshed cache gains the response ETag.
func TestMaybeUpdateNoticeOldCacheFullFetch(t *testing.T) {
	withFakeVersion(t, "0.1.0")
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	var sawINM bool
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/synuns/flint/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		sawINM = r.Header.Get("If-None-Match") != ""
		w.Header().Set("ETag", `"v1"`)
		fmt.Fprint(w, `{"tag_name":"v9.9.9","assets":[]}`)
	})
	srv := httptest.NewTLSServer(mux)
	prevClient := updateClientFn
	updateClientFn = func(timeout time.Duration) *http.Client {
		c := srv.Client()
		c.Timeout = timeout
		return c
	}
	prev := updateAPIBase
	updateAPIBase = srv.URL
	t.Cleanup(func() { updateAPIBase = prev; updateClientFn = prevClient })
	t.Cleanup(srv.Close)
	// old-format cache: no etag key at all, expired.
	old := `{"latest":"0.0.1","checked_at":"` + time.Now().UTC().Add(-48*time.Hour).Format(time.RFC3339) + `"}`
	if err := os.WriteFile(updateCacheFile(), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := maybeUpdateNotice(); !strings.Contains(got, "flint update") {
		t.Fatalf("구 캐시 풀 fetch 안내 없음: %q", got)
	}
	if sawINM {
		t.Error("ETag 없는 캐시인데 If-None-Match 전송")
	}
	if after := readUpdateCache(t); after.Latest != "9.9.9" || after.ETag != `"v1"` {
		t.Errorf("풀 fetch 캐시 갱신 실패: %+v", after)
	}
}

// TestMaybeUpdateNoticeNetFailSilent — a dead endpoint is silent on both the
// expiry path and the contradiction path, and the cache is left untouched.
func TestMaybeUpdateNoticeNetFailSilent(t *testing.T) {
	withFakeVersion(t, "0.1.0")
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	srv := httptest.NewServer(http.NotFoundHandler())
	dead := srv.URL
	srv.Close() // refused endpoint
	prev := updateAPIBase
	updateAPIBase = dead
	t.Cleanup(func() { updateAPIBase = prev })
	// (a) expired cache -> silent, cache untouched.
	stale := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	writeUpdateCache(t, updateCache{Latest: "0.0.1", CheckedAt: stale})
	if got := maybeUpdateNotice(); got != "" {
		t.Errorf("만료+네트워크 실패 조용히 위반: %q", got)
	}
	if after := readUpdateCache(t); after.CheckedAt != stale {
		t.Errorf("실패 시 캐시 침범: %+v", after)
	}
	// (b) contradiction (fresh cache, latest behind) + failure -> still silent.
	writeUpdateCache(t, updateCache{Latest: "0.0.1", CheckedAt: time.Now().UTC().Format(time.RFC3339)})
	if got := maybeUpdateNotice(); got != "" {
		t.Errorf("모순+네트워크 실패 조용히 위반: %q", got)
	}
}

// TestNoticeLineCheckedAt — the notice line exposes checked_at when known;
// no notice when the running version is not behind.
func TestNoticeLineCheckedAt(t *testing.T) {
	ts := "2026-11-23T00:00:00Z"
	got := noticeLine("9.9.9", "0.1.0", ts)
	if !strings.Contains(got, "v9.9.9") || !strings.Contains(got, ts) {
		t.Errorf("checked_at 미표시: %q", got)
	}
	if noticeLine("0.1.0", "0.1.0", ts) != "" {
		t.Error("같은 버전인데 안내 출력")
	}
}

// ── v1.26.0 T3 — the mcp-registry published-version fetch (doctor versions)
// and the updateCacheInfo read for the doctor/report display. ──

// withRegistryServer — a fake raw.githubusercontent plugin.json endpoint
// honoring If-None-Match (304 on exact match) and counting requests.
func withRegistryServer(t *testing.T, version, etag string) *int32 {
	t.Helper()
	var hits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/synuns/flint/main/flint-plugin/.claude-plugin/plugin.json", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if etag != "" && r.Header.Get("If-None-Match") == etag {
			w.Header().Set("ETag", etag)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		fmt.Fprintf(w, `{"name":"flint","version":%q}`, version)
	})
	srv := httptest.NewTLSServer(mux)
	prevClient := updateClientFn
	updateClientFn = func(timeout time.Duration) *http.Client {
		c := srv.Client()
		c.Timeout = timeout
		return c
	}
	prevURL := updateRegistryURL
	updateRegistryURL = srv.URL + "/synuns/flint/main/flint-plugin/.claude-plugin/plugin.json"
	t.Cleanup(func() { updateRegistryURL = prevURL; updateClientFn = prevClient })
	t.Cleanup(srv.Close)
	return &hits
}

// TestFetchRegistryVersionETagRoundTrip — the first call gets the published
// version (cached with the ETag); the second sends If-None-Match, and the 304
// keeps the cached version (checked_at refreshed).
func TestFetchRegistryVersionETagRoundTrip(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	const etag = `"plug1"`
	hits := withRegistryServer(t, "1.26.0", etag)
	v, err := fetchRegistryVersion()
	if err != nil || v != "1.26.0" {
		t.Fatalf("1차 fetch = %q %v, want 1.26.0", v, err)
	}
	var c registryCache
	b, _ := os.ReadFile(registryCacheFile())
	_ = json.Unmarshal(b, &c)
	if c.Version != "1.26.0" || c.ETag != etag {
		t.Errorf("레지스트리 캐시 저장 실패: %+v", c)
	}
	// 304 — the cached version carries over, checked_at refreshed.
	v2, err := fetchRegistryVersion()
	if err != nil || v2 != "1.26.0" {
		t.Fatalf("304 승계 = %q %v, want 1.26.0", v2, err)
	}
	if n := atomic.LoadInt32(hits); n != 2 {
		t.Errorf("요청 수 = %d, want 2", n)
	}
	var after registryCache
	rb, _ := os.ReadFile(registryCacheFile())
	_ = json.Unmarshal(rb, &after)
	checked, terr := time.Parse(time.RFC3339, after.CheckedAt)
	if terr != nil || time.Since(checked) > time.Minute {
		t.Errorf("checked_at 미갱신: %q %v", after.CheckedAt, terr)
	}
}

// TestFetchRegistryVersionOffline — a refused endpoint is an error (the
// doctor caller renders "unverified (offline)" — a note, not a warning).
func TestFetchRegistryVersionOffline(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	prev := updateRegistryURL
	updateRegistryURL = url
	t.Cleanup(func() { updateRegistryURL = prev })
	if _, err := fetchRegistryVersion(); err == nil {
		t.Error("오프라인 에러 아님")
	}
}

// TestUpdateCacheInfo — the doctor-facing cache read round-trips the update
// cache (latest + checked_at); no cache yields empty strings.
func TestUpdateCacheInfo(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if latest, checked := updateCacheInfo(); latest != "" || checked != "" {
		t.Errorf("캐시 부재 시 = %q %q, want 빈값", latest, checked)
	}
	ts := time.Now().UTC().Add(-3 * time.Hour).Format(time.RFC3339)
	writeUpdateCache(t, updateCache{Latest: "1.26.0", CheckedAt: ts})
	latest, checked := updateCacheInfo()
	if latest != "1.26.0" || checked != ts {
		t.Errorf("왕복 = %q %q, want 1.26.0 %s", latest, checked, ts)
	}
}

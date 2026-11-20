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

// TestMaybeUpdateNoticeCache — within the cache TTL the check is decided without network;
// on expiry it re-checks and refreshes the cache.
func TestMaybeUpdateNoticeCache(t *testing.T) {
	withFakeVersion(t, "0.1.0")
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	// fresh cache: latest is behind -> no output (must make no calls even with a dead server).
	fresh, _ := os.ReadFile(updateCacheFile())
	_ = fresh
	c := updateCache{Latest: "0.0.1", CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	b, _ := json.Marshal(c)
	os.WriteFile(updateCacheFile(), b, 0o600)
	// dead server — within the TTL there must be no call at all.
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

// update_test.go — v0.22.0 자동 업데이트 계약: 버전 비교·체크섬·추출·
// 전체 흐름(htptest 주입)·갱신 안내 캐시.
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

// withFakeVersion — version 패키지 변수 주입(테스트 바이너리는 ldflags
// 미주입으로 "dev"이므로).
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
	if _, err := checksumFor(sums, "없음.tar.gz"); err == nil {
		t.Error("부재 항목 에러 아님")
	}
}

// buildTarGz — flint 바이너리 1개를 담은 tar.gz 픽스처.
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

// newUpdateServer — 가짜 릴리스 API+자산 서버. newBin은 새 바이너리
// 내용(실행 시 version JSON을 출력하는 셸 스크립트여야 검증 통과).
func newUpdateServer(t *testing.T, latest string, newBin []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	assetName := fmt.Sprintf("flint_%s_%s_%s.tar.gz", latest, runtime.GOOS, runtime.GOARCH)
	archive := buildTarGz(t, newBin)
	mux.HandleFunc("/repos/synuns/flint/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v%s","assets":[{"name":%q,"browser_download_url":%q},{"name":"checksums.txt","browser_download_url":%q}]}`,
			latest, assetName, srv.URL+"/dl/"+assetName, srv.URL+"/dl/checksums.txt")
	})
	mux.HandleFunc("/dl/"+assetName, func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	})
	// sha256은 archive 바이트 기반 — 핸들러 내 계산을 위해 클로저로.
	sum := sha256Hex(archive)
	mux.HandleFunc("/dl/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", sum, assetName)
	})
	return srv
}

// withUpdateServer — API 베이스 주입 공통 절차.
func withUpdateServer(t *testing.T, latest string, newBin []byte) {
	t.Helper()
	srv := newUpdateServer(t, latest, newBin)
	prev := updateAPIBase
	updateAPIBase = srv.URL
	t.Cleanup(func() { updateAPIBase = prev })
}

// TestUpdateFlowReplacesBinary — 전체 흐름: 확인→다운로드→검증→교체.
// executablePathFn을 임시 '현재 바이너리'로 주입해 실제 파일 교체를
// 검증한다(검증 통과용 새 바이너리는 version 출력 셸 스크립트).
func TestUpdateFlowReplacesBinary(t *testing.T) {
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

// TestUpdateFlowChecksumMismatch — 체크섬 불일치면 원본 불본(즉시 중단).
func TestUpdateFlowChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "flint")
	if err := os.WriteFile(self, []byte("original"), 0o755); err != nil {
		t.Fatal(err)
	}
	prevExe := executablePathFn
	executablePathFn = func() (string, error) { return self, nil }
	t.Cleanup(func() { executablePathFn = prevExe })

	newBin := []byte("#!/bin/sh\necho '{\"version\":\"9.9.9\"}'\n")
	// 정상 릴리스 서버 + 오염 checksums(해시 불일치) 재정의.
	srv := newUpdateServer(t, "9.9.9", newBin)
	mux := http.NewServeMux()
	assetName := fmt.Sprintf("flint_%s_%s_%s.tar.gz", "9.9.9", runtime.GOOS, runtime.GOARCH)
	mux.HandleFunc("/repos/synuns/flint/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v9.9.9","assets":[{"name":%q,"browser_download_url":%q},{"name":"checksums.txt","browser_download_url":%q}]}`,
			assetName, srv.URL+"/dl/"+assetName, srv.URL+"/bad")
	})
	mux.HandleFunc("/bad", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "0000000000000000000000000000000000000000000000000000000000000000  "+assetName+"\n")
	})
	badSrv := httptest.NewServer(mux)
	t.Cleanup(badSrv.Close)
	prev := updateAPIBase
	updateAPIBase = badSrv.URL
	t.Cleanup(func() { updateAPIBase = prev })

	if _, err := applyUpdate("9.9.9"); err == nil {
		t.Fatal("체크섬 불일치인데 성공")
	}
	got, _ := os.ReadFile(self)
	if string(got) != "original" {
		t.Fatalf("불일치 경로에서 원본 훼손: %q", got)
	}
}

// TestMaybeUpdateNoticeCache — 캐시 TTL 내 확인은 네트워크 없이 판정,
// 만료 시 재확인 후 캐시 갱신.
func TestMaybeUpdateNoticeCache(t *testing.T) {
	withFakeVersion(t, "0.1.0")
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	// 신선 캐시: 최신이 뒤처림 → 무출력(서버 죽어도 무호출이어야).
	fresh, _ := os.ReadFile(updateCacheFile())
	_ = fresh
	c := updateCache{Latest: "0.0.1", CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	b, _ := json.Marshal(c)
	os.WriteFile(updateCacheFile(), b, 0o600)
	// 죽은 서버 — TTL 내면 호출 자체가 없어야 한다.
	withUpdateServer(t, "9.9.9", nil)
	if got := maybeUpdateNotice(); got != "" {
		t.Errorf("캐시 무시·무출력 위반: %q", got)
	}
	// 만료 캐시 + 최신 9.9.9 → 안내 1줄 + 캐시 갱신.
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

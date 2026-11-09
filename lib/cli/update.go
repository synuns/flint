// update.go — v0.22.0 자동 업데이트(A): `flint update` 자가 교체 +
// 세션 시작 갱신 안내 + doctor update 체크. 원칙: 다운로드·교체는 오직
// 명시적 실행 시만(안내는 해도 자동 실행 없음 — §13 비간섭). 네트워크
// 실패는 조용히, 체크섬 불일치는 즉시 중단(ADR-0006 취지 승계).
// 검증 후 교체(verify-then-swap): 새 바이너리가 `version`을 정상 출력해야
// rename하므로 실패 경로는 원본을 건드리지 않는다.
package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func init() { Register("update", runUpdateCmd) }

// updateAPIBase — GitHub Releases API 기본(테스트는 httptest로 주입).
var updateAPIBase = "https://api.github.com"

// executablePathFn — 자가 경로 조회 seam(테스트는 임시 파일로 주입 —
// 테스트 바이너리 자체를 교체하지 않게).
var executablePathFn = os.Executable

// updateAPITimeout — Releases API 조회 타임아웃(단발 소형 — 오프라인
// 환경에서 훅·doctor가 길게 걸리지 않게 짧게).
const updateAPITimeout = 5 * time.Second

// updateHTTPTimeout — 자산 다운로드 클라이언트 타임아웃.
const updateHTTPTimeout = 30 * time.Second

// updateClientFn — 클라이언트 팩토리 seam(v0.22.2 리뷰 ③: 프로덕션
// 바이너리에서 InsecureSkipVerify를 완전히 제거 — 테스트는 httptest
// srv.Client() 주입으로 자체 서명을 수용한다).
var updateClientFn = func(timeout time.Duration) *http.Client { return updateClient(timeout) }

// updateClient — API·다운로드 공통 클라이언트. DefaultTransport 복제로
// ProxyFromEnvironment 유지(리뷰 ① — 프록시 환경 단절 회귀 방지),
// 리다이렉트는 https만 추적(리뷰 ② — https→http 다운그레이드 방어).
func updateClient(timeout time.Duration) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	return &http.Client{
		Timeout:   timeout,
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" {
				return fmt.Errorf("비-https 리다이렉트 거부: %s", req.URL)
			}
			return nil
		},
	}
}

// updateNoticeTTL — 세션 시작 갱신 확인 캐시 수명(하루 1회 확인).
const updateNoticeTTL = 24 * time.Hour

// updateCacheFile — 갱신 확인 캐시 경로(configDir — 마커 패턴 승계).
func updateCacheFile() string {
	return dataFile("update-check.json")
}

// updateCache — 갱신 확인 캐시 1행.
type updateCache struct {
	Latest    string `json:"latest"`
	CheckedAt string `json:"checked_at"`
}

// runUpdateCmd — `flint update` 진입: --check 드라이런, 기본은 확인·
// 다운로드·검증·교체. store-free(네트워크·파일시스템만).
func runUpdateCmd(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	dry := false
	for _, a := range args {
		switch a {
		case "--check":
			dry = true
		default:
			fmt.Fprintln(stdout, errLine(fmt.Errorf("unknown flag: %s", a)))
			return 1
		}
	}
	latest, err := fetchLatestVersion()
	if err != nil {
		// 네트워크 실패 — 조용한 계약은 안내 경로(훅)에만 적용, 명시적
		// 실행이므로 사유를 알린다(exit 1).
		fmt.Fprintln(stdout, errLine(fmt.Errorf("update: 최신 버전 확인 실패: %w", err)))
		return 1
	}
	cur := CurrentVersion()
	if !validVersion(cur) {
		fmt.Fprintln(stdout, `{"status":"unknown","detail":"현재 빌드 버전 미확정(dev) — 업데이트 대상 아님"}`)
		return 0
	}
	if compareVersions(latest, cur) <= 0 {
		fmt.Fprintln(stdout, `{"status":"up-to-date","version":`+quoteJSON(cur)+"}")
		return 0
	}
	if dry {
		fmt.Fprintln(stdout, `{"status":"update-available","current":`+quoteJSON(cur)+`,"latest":`+quoteJSON(latest)+"}")
		return 0
	}
	path, err := applyUpdate(latest)
	if err != nil {
		fmt.Fprintln(stdout, errLine(fmt.Errorf("update: %w", err)))
		return 1
	}
	fmt.Fprintln(stdout, `{"status":"updated","from":`+quoteJSON(cur)+`,"to":`+quoteJSON(latest)+`,"path":`+quoteJSON(path)+"}")
	return 0
}

// releaseMeta — releases/latest 응답의 필요 필드만.
type releaseMeta struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// fetchLatestVersion — 최신 태그(v 접두 제거) 조회. 캐시 갱신은 호출부.
func fetchLatestVersion() (string, error) {
	meta, err := fetchReleaseMeta()
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(meta.TagName, "v"), nil
}

// fetchReleaseMeta — releases/latest 조회(공식 API만).
func fetchReleaseMeta() (*releaseMeta, error) {
	client := updateClientFn(updateAPITimeout)
	resp, err := client.Get(updateAPIBase + "/repos/synuns/flint/releases/latest")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api status %d", resp.StatusCode)
	}
	var meta releaseMeta
	// v0.22.2 리뷰 ⑤: API 응답도 상한 해석(무한 JSON 적립 방어 — 릴리스
	// 메타는 수 KB 규모라 8MiB면 충분).
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&meta); err != nil {
		return nil, err
	}
	if meta.TagName == "" {
		return nil, errors.New("tag_name 부재")
	}
	return &meta, nil
}

// applyUpdate — 자산 다운로드·sha256 검증(checksums.txt)·바이너리 추출·
// version 실행 검증·원자 교체(rename). 모든 실패는 원본 불변.
func applyUpdate(latest string) (string, error) {
	meta, err := fetchReleaseMeta()
	if err != nil {
		return "", err
	}
	asset := fmt.Sprintf("flint_%s_%s_%s.tar.gz", latest, runtime.GOOS, runtime.GOARCH)
	var assetURL, sumsURL string
	for _, a := range meta.Assets {
		switch a.Name {
		case asset:
			assetURL = a.BrowserDownloadURL
		case "checksums.txt":
			sumsURL = a.BrowserDownloadURL
		}
	}
	if assetURL == "" || sumsURL == "" {
		return "", fmt.Errorf("자산 부재(%s·checksums.txt)", asset)
	}
	body, err := download(assetURL)
	if err != nil {
		return "", fmt.Errorf("다운로드: %w", err)
	}
	sums, err := download(sumsURL)
	if err != nil {
		return "", fmt.Errorf("checksums 다운로드: %w", err)
	}
	want, err := checksumFor(string(sums), asset)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	if got := hex.EncodeToString(digest[:]); got != want {
		return "", fmt.Errorf("체크섬 불일치 — 중단(want %s got %s)", want, got)
	}
	bin, err := extractBinary(body)
	if err != nil {
		return "", err
	}
	return swapBinary(bin)
}

// updateMaxBytes — 자산·체크섬 다운로드 상한(v0.22.1 패치 루프 1차 S2 —
// io.ReadAll 무한 적립 방어. 릴리스 자산은 통상 수 MB — 여유 상한).
const updateMaxBytes = 128 << 20 // 128MiB

// download — 전체 다운로드(자산·체크섬 모두 소형·단발).
// v0.22.1(S1): 다운로드 URL은 https 강제 — API 응답의 browser_download_url이
// 평문 스킴을 가리키면 거부한다(응답 변조·다운그레이드 방지).
func download(url string) ([]byte, error) {
	if !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("비-https 다운로드 URL 거부: %s", url)
	}
	client := updateClientFn(updateHTTPTimeout)
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d (%s)", resp.StatusCode, url)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, updateMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > updateMaxBytes {
		return nil, fmt.Errorf("다운로드 상한 %d바이트 초과 — 중단", updateMaxBytes)
	}
	return body, nil
}

// checksumFor — sha256sum 관련 checksums.txt에서 해당 파일 해시 추출.
func checksumFor(sums, name string) (string, error) {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.TrimPrefix(fields[1], "*") == name {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksums.txt에 %s 없음", name)
}

// extractBinary — tar.gz에서 루트 flint 바이너리 추출.
func extractBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("아카이브에 flint 바이너리 없음")
		}
		if err != nil {
			return nil, fmt.Errorf("tar: %w", err)
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == "flint" {
			// v0.28.0(H④): 해제 스트림에도 동일 상한 — 압축 바이트 상한만으론
			// gzip 폭발(작은 압축분의 수십 GiB 팽창)을 막지 못한다. 상한+1
			// 읽기로 초과를 명시적 에러 처리(무음 절단 금지 — 리뷰 4).
			b, rerr := io.ReadAll(io.LimitReader(tr, updateMaxBytes+1))
			if rerr != nil {
				return nil, fmt.Errorf("아카이브 읽기: %w", rerr)
			}
			if int64(len(b)) > updateMaxBytes {
				return nil, fmt.Errorf("아카이브 항목이 %dMiB 상한 초과", updateMaxBytes>>20)
			}
			return b, nil
		}
	}
}

// swapBinary — 새 바이너리를 임시 파일로 검증(version 실행) 후 원자 교체.
func swapBinary(bin []byte) (string, error) {
	self, err := executablePathFn()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(self)
	tmp, err := os.CreateTemp(dir, ".flint-update-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // rename 성공 시 no-op(이동됨)
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(self); err == nil {
		mode = fi.Mode()
	}
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return "", err
	}
	// 검증 후 교체 — 새 바이너리가 version을 출력해야 rename한다.
	out, err := exec.Command(tmpPath, "version").Output()
	if err != nil || !strings.Contains(string(out), `"version"`) {
		return "", fmt.Errorf("새 바이너리 검증 실패(version 미출력): %v", err)
	}
	if err := os.Rename(tmpPath, self); err != nil {
		return "", fmt.Errorf("교체(rename): %w", err)
	}
	return self, nil
}

// validVersion — x.y.z 형식(dev 등 미주입 빌드 판별).
func validVersion(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if _, err := strconv.Atoi(p); err != nil {
			return false
		}
	}
	return true
}

// compareVersions — a>b면 1, a<b면 -1, 동일 0(x.y.z 수치 비교).
func compareVersions(a, b string) int {
	ap, bp := versionParts(a), versionParts(b)
	for i := range ap {
		if ap[i] != bp[i] {
			if ap[i] > bp[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}

// versionParts — x.y.z → [3]int(무효는 0 취급 — 방어).
func versionParts(v string) [3]int {
	var out [3]int
	for i, p := range strings.Split(v, ".") {
		if i > 2 {
			break
		}
		n, _ := strconv.Atoi(p)
		out[i] = n
	}
	return out
}

// maybeUpdateNotice — 세션 시작 갱신 안내(v0.22.0 R2): 캐시(TTL 24h)가
// 만료된 경우만 확인(하루 1회 왕복), 최신>현재면 [flint] 경계 내 1줄.
// 네트워크 실패·캐시 부재는 조용히(§13 — 안내 부재가 곧 무해).
func maybeUpdateNotice() string {
	cur := CurrentVersion()
	if !validVersion(cur) {
		return ""
	}
	path := updateCacheFile()
	if path == "" {
		return ""
	}
	var c updateCache
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &c)
		if checked, err := time.Parse(time.RFC3339, c.CheckedAt); err == nil &&
			time.Since(checked) < updateNoticeTTL {
			return noticeLine(c.Latest, cur)
		}
	}
	latest, err := fetchLatestVersion()
	if err != nil {
		return "" // 조용히 — 캐시도 갱신하지 않는다(다음 세션 재시도)
	}
	c = updateCache{Latest: latest, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	if b, err := json.Marshal(c); err == nil {
		_ = os.WriteFile(path, b, 0o600)
	}
	return noticeLine(latest, cur)
}

// noticeLine — 최신이 앞서면 안내 1줄, 아니면 빈 문자열.
func noticeLine(latest, cur string) string {
	if latest != "" && validVersion(latest) && compareVersions(latest, cur) > 0 {
		return "[flint] 새 버전 v" + latest + " — flint update"
	}
	return ""
}

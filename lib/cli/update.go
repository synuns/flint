// update.go — auto-update: `flint update` self-replacement +
// session-start update notice + the doctor update check. Principle: download and replacement happen only
// on explicit invocation (notices are fine, never auto-execution — §13 non-interference). Network
// failures are silent; checksum mismatch aborts immediately (carrying over the ADR-0006 intent).
// verify-then-swap: the new binary must run `version` successfully before the
// rename, so failure paths never touch the original.
package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed"

	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/synuns/flint/lib/wire"
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

// updateAPIBase — the GitHub Releases API base (tests inject httptest).
var updateAPIBase = "https://api.github.com"

// executablePathFn — self-path lookup seam (tests inject a temp file —
// so the test binary itself is never replaced).
var executablePathFn = os.Executable

// updateAPITimeout — Releases API lookup timeout (small and one-shot — kept short so
// hooks and doctor don't hang long in offline environments).
const updateAPITimeout = 5 * time.Second

// updateHTTPTimeout — asset download client timeout.
const updateHTTPTimeout = 30 * time.Second

// updateClientFn — client factory seam (removes InsecureSkipVerify
// entirely from production binaries — tests accept self-signed certs by injecting
// httptest srv.Client()).
var updateClientFn = func(timeout time.Duration) *http.Client { return updateClient(timeout) }

// updateClient — shared client for the API and downloads. Clones DefaultTransport to
// keep ProxyFromEnvironment (review 1 — prevents a proxy-environment regression),
// and follows https-only redirects (review 2 — defends against https->http downgrade).
func updateClient(timeout time.Duration) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	return &http.Client{
		Timeout:   timeout,
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" {
				return fmt.Errorf("non-https redirect rejected: %s", req.URL)
			}
			return nil
		},
	}
}

// updateNoticeTTL — lifetime of the session-start update-check cache (one check per day).
const updateNoticeTTL = 24 * time.Hour

// updateCacheFile — update-check cache path (configDir — carrying over the marker pattern).
func updateCacheFile() string {
	return dataFile("update-check.json")
}

// updateCache — one line of update-check cache.
type updateCache struct {
	Latest    string `json:"latest"`
	CheckedAt string `json:"checked_at"`
}

// runUpdateCmd — `flint update` entry: --check dry-run; default is check,
// download, verify, and replace. store-free (network and filesystem only).
// updateSkipSignature — --insecure-skip-signature (a one-time explicit bypass for trust
// transitions such as key rolling — ADR-0014). It lives as process state, so it is reset on every entry
// in preparation for a future change where re-entrant callers like MCP expose
// update.
// unknown flags are invalid-arg exit 3 (validated before network access).
var updateSkipSignature = false

func runUpdateCmd(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// moved the manual loop to the wire.ParseArgs spec table (positional args
	// not allowed — keeping the validate-before-network-access contract).
	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--check"},
			{Name: "--insecure-skip-signature"},
		},
		Usage: "usage: flint update [--check] [--insecure-skip-signature]",
	})
	if perr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	dry := res.Given("--check")
	updateSkipSignature = false // entry reset
	if res.Given("--insecure-skip-signature") {
		updateSkipSignature = true
		fmt.Fprintln(os.Stderr, "[flint] update: signature verification bypass (one-time trust transfer — ADR-0014 key rollover procedure)")
	}
	latest, err := fetchLatestVersion()
	if err != nil {
		// network failure — the silent contract applies only to the notice path (hooks); this is an
		// explicit invocation, so report the reason (exit 1).
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("update: failed to check the latest version: %w", err)))
		return 1
	}
	cur := CurrentVersion()
	if !validVersion(cur) {
		fmt.Fprintln(stdout, `{"status":"unknown","detail":"current build version unknown (dev) — not an update target"}`)
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
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("update: %w", err)))
		return 1
	}
	fmt.Fprintln(stdout, `{"status":"updated","from":`+quoteJSON(cur)+`,"to":`+quoteJSON(latest)+`,"path":`+quoteJSON(path)+"}")
	return 0
}

// releaseMeta — only the needed fields of the releases/latest response.
type releaseMeta struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// releasePubPEM — public key for release signature verification (the trust root — ADR-0014).
// lib/cli/cosign.pub embedded at build time. An empty file = unconfigured transition mode
// (even with a .sig present, only warn and pass). With the public key embedded, hard verification:
// missing .sig, mismatch, or scheme errors are all rejected (fail-closed).
//
//go:embed cosign.pub
var releasePubPEM []byte

// fetchLatestVersion — looks up the latest tag (v prefix stripped). Cache refresh is the caller's job.
func fetchLatestVersion() (string, error) {
	meta, err := fetchReleaseMeta()
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(meta.TagName, "v"), nil
}

// fetchReleaseMeta — releases/latest lookup (official API only).
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
	// the API response is also read with a cap (defense against unbounded JSON accumulation — release
	// metadata is a few KB, so 8MiB is plenty).
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&meta); err != nil {
		return nil, err
	}
	if meta.TagName == "" {
		return nil, errors.New("missing tag_name")
	}
	return &meta, nil
}

// applyUpdate — asset download, sha256 verification (checksums.txt), binary extraction,
// version-run verification, and atomic replacement (rename). Every failure leaves the original untouched.
func applyUpdate(latest string) (string, error) {
	meta, err := fetchReleaseMeta()
	if err != nil {
		return "", err
	}
	asset := fmt.Sprintf("flint_%s_%s_%s.tar.gz", latest, runtime.GOOS, runtime.GOARCH)
	var assetURL, sumsURL, sigURL string
	for _, a := range meta.Assets {
		switch a.Name {
		case asset:
			assetURL = a.BrowserDownloadURL
		case "checksums.txt":
			sumsURL = a.BrowserDownloadURL
		case "checksums.txt.sig":
			sigURL = a.BrowserDownloadURL // use the listed URL — never assemble one
		}
	}
	if assetURL == "" || sumsURL == "" {
		return "", fmt.Errorf("missing assets (%s·checksums.txt)", asset)
	}
	body, err := download(assetURL)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	sums, err := download(sumsURL)
	if err != nil {
		return "", fmt.Errorf("checksums download: %w", err)
	}
	// (ADR-0014): checksums.txt signature verification — a trust root separate from the
	// GitHub origin (embedded public key). The asset list decides whether a .sig exists:
	//   - public key embedded (hard verification): missing .sig, mismatch, or scheme errors are all rejected
	//     (fail-closed — blocks .sig deletion and garbage-signature evasion)
	//   - unconfigured (transition mode): with a .sig, warn and proceed; without, proceed
	// trust transitions such as key rolling use --insecure-skip-signature (explicit user opt-in).
	verifier := ReleaseVerifier{PublicPEM: releasePubPEM}
	if updateSkipSignature {
		fmt.Fprintln(os.Stderr, "[flint] update: signature verification bypass (--insecure-skip-signature)")
		verifier = ReleaseVerifier{} // treated as unconfigured — the path passes
	}
	if sigURL != "" {
		sig, serr := download(sigURL)
		if serr != nil || len(sig) == 0 {
			if verifier.Configured() {
				return "", fmt.Errorf("signature asset download failed — update refused (fail-closed)")
			}
		} else if verr := verifier.Verify(sums, string(sig)); verr != nil {
			if verifier.Configured() || errors.Is(verr, ErrSignatureInvalid) {
				return "", fmt.Errorf("release signature error — update refused: %v", verr)
			}
			fmt.Fprintf(os.Stderr, "[flint] update: signature verification warning (transition mode): %v\n", verr)
		}
	} else if verifier.Configured() {
		return "", fmt.Errorf("missing signature asset (checksums.txt.sig) — update refused (hard verification; for key rollover use --insecure-skip-signature)")
	}
	want, err := checksumFor(string(sums), asset)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	if got := hex.EncodeToString(digest[:]); got != want {
		return "", fmt.Errorf("checksum mismatch — aborting (want %s got %s)", want, got)
	}
	bin, err := extractBinary(body)
	if err != nil {
		return "", err
	}
	return swapBinary(bin)
}

// updateMaxBytes — download cap for assets and checksums (
// defense against unbounded io.ReadAll accumulation. Release assets are usually a few MB — a generous cap).
const updateMaxBytes = 128 << 20 // 128MiB

// download — full download (both asset and checksum are small, one-shot).
// download URLs force https — if browser_download_url in the API response
// points to a plaintext scheme, reject it (prevents response tampering and downgrade).
func download(url string) ([]byte, error) {
	if !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("non-https download URL rejected: %s", url)
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
		return nil, fmt.Errorf("download exceeds %d-byte limit — aborting", updateMaxBytes)
	}
	return body, nil
}

// checksumFor — extracts the file's hash from the sha256sum-style checksums.txt.
func checksumFor(sums, name string) (string, error) {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.TrimPrefix(fields[1], "*") == name {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("%s not found in checksums.txt", name)
}

// extractBinary — extracts the root flint binary from the tar.gz.
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
			return nil, errors.New("flint binary not found in archive")
		}
		if err != nil {
			return nil, fmt.Errorf("tar: %w", err)
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == "flint" {
			// the same cap on the decompressed stream too — a compressed-byte cap alone cannot
			// stop a gzip bomb (a small archive expanding to tens of GiB). Read cap+1
			// bytes to turn overflow into an explicit error (no silent truncation).
			b, rerr := io.ReadAll(io.LimitReader(tr, updateMaxBytes+1))
			if rerr != nil {
				return nil, fmt.Errorf("archive read: %w", rerr)
			}
			if int64(len(b)) > updateMaxBytes {
				return nil, fmt.Errorf("archive entry exceeds %dMiB limit", updateMaxBytes>>20)
			}
			return b, nil
		}
	}
}

// swapBinary — verifies the new binary via a temp file (running version), then atomically replaces it.
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
	defer os.Remove(tmpPath) // no-op after a successful rename (it moved)
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
	// verify-then-swap — the new binary must print version before the rename.
	out, err := exec.Command(tmpPath, "version").Output()
	if err != nil || !strings.Contains(string(out), `"version"`) {
		return "", fmt.Errorf("new binary verification failed (no version output): %v", err)
	}
	if err := os.Rename(tmpPath, self); err != nil {
		return "", fmt.Errorf("replace (rename): %w", err)
	}
	return self, nil
}

// validVersion — x.y.z form (detects uninjected builds like dev).
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

// compareVersions — 1 if a>b, -1 if a<b, 0 if equal (numeric x.y.z comparison).
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

// versionParts — x.y.z -> [3]int (invalid treated as 0 — defensive).
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

// maybeUpdateNotice — session-start update notice: check only when the cache (24h TTL) has
// expired (one round trip per day); if latest > current, one line inside the [flint] banner.
// Network failure or missing cache is silent (§13 — no notice is harmless by definition).
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
		return "" // silent — the cache is not refreshed either (retry next session)
	}
	c = updateCache{Latest: latest, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	if b, err := json.Marshal(c); err == nil {
		_ = os.WriteFile(path, b, 0o600)
	}
	return noticeLine(latest, cur)
}

// noticeLine — one notice line if a newer release is ahead, else the empty string.
func noticeLine(latest, cur string) string {
	if latest != "" && validVersion(latest) && compareVersions(latest, cur) > 0 {
		return "[flint] new version v" + latest + " — flint update"
	}
	return ""
}

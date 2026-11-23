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

var updateAPIBase = "https://api.github.com"

var executablePathFn = os.Executable

const updateAPITimeout = 5 * time.Second

const updateHTTPTimeout = 30 * time.Second

var updateClientFn = func(timeout time.Duration) *http.Client { return updateClient(timeout) }

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

const updateNoticeTTL = 24 * time.Hour

func updateCacheFile() string {
	return dataFile("update-check.json")
}

type updateCache struct {
	Latest    string `json:"latest"`
	ETag      string `json:"etag,omitempty"`
	CheckedAt string `json:"checked_at"`
}

var updateSkipSignature = false

func runUpdateCmd(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {

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
	updateSkipSignature = false
	if res.Given("--insecure-skip-signature") {
		updateSkipSignature = true
		fmt.Fprintln(os.Stderr, "[flint] update: signature verification bypass (one-time trust transfer — ADR-0014 key rollover procedure)")
	}
	latest, err := fetchLatestVersion()
	if err != nil {

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

type releaseMeta struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

//go:embed cosign.pub
var releasePubPEM []byte

func fetchLatestVersion() (string, error) {
	meta, etag, err := fetchReleaseMetaETag("")
	if err != nil {
		return "", err
	}
	latest := strings.TrimPrefix(meta.TagName, "v")
	if path := updateCacheFile(); path != "" {
		c := updateCache{Latest: latest, ETag: etag, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
		if b, merr := json.Marshal(c); merr == nil {
			_ = os.WriteFile(path, b, 0o600)
		}
	}
	return latest, nil
}

func fetchReleaseMeta() (*releaseMeta, error) {
	meta, _, err := fetchReleaseMetaETag("")
	return meta, err
}

func fetchReleaseMetaETag(etag string) (*releaseMeta, string, error) {
	client := updateClientFn(updateAPITimeout)
	req, err := http.NewRequest(http.MethodGet, updateAPIBase+"/repos/synuns/flint/releases/latest", nil)
	if err != nil {
		return nil, "", err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	newEtag := resp.Header.Get("ETag")
	if resp.StatusCode == http.StatusNotModified {

		if newEtag == "" {
			newEtag = etag
		}
		return nil, newEtag, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("api status %d", resp.StatusCode)
	}
	var meta releaseMeta

	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&meta); err != nil {
		return nil, "", err
	}
	if meta.TagName == "" {
		return nil, "", errors.New("missing tag_name")
	}
	return &meta, newEtag, nil
}

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
			sigURL = a.BrowserDownloadURL
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

	verifier := ReleaseVerifier{PublicPEM: releasePubPEM}
	if updateSkipSignature {
		fmt.Fprintln(os.Stderr, "[flint] update: signature verification bypass (--insecure-skip-signature)")
		verifier = ReleaseVerifier{}
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

const updateMaxBytes = 128 << 20

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

func checksumFor(sums, name string) (string, error) {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.TrimPrefix(fields[1], "*") == name {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("%s not found in checksums.txt", name)
}

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
	defer os.Remove(tmpPath)
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

	out, err := exec.Command(tmpPath, "version").Output()
	if err != nil || !strings.Contains(string(out), `"version"`) {
		return "", fmt.Errorf("new binary verification failed (no version output): %v", err)
	}
	if err := os.Rename(tmpPath, self); err != nil {
		return "", fmt.Errorf("replace (rename): %w", err)
	}
	return self, nil
}

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
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &c) == nil {
		if checked, terr := time.Parse(time.RFC3339, c.CheckedAt); terr == nil &&
			time.Since(checked) < updateNoticeTTL &&
			compareVersions(c.Latest, cur) >= 0 {

			return noticeLine(c.Latest, cur, c.CheckedAt)
		}

	}
	meta, etag, err := fetchReleaseMetaETag(c.ETag)
	if err != nil {
		return ""
	}
	latest := c.Latest
	if meta != nil {
		latest = strings.TrimPrefix(meta.TagName, "v")
	}
	if latest == "" {
		return ""
	}
	c = updateCache{Latest: latest, ETag: etag, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	if b, merr := json.Marshal(c); merr == nil {
		_ = os.WriteFile(path, b, 0o600)
	}
	return noticeLine(latest, cur, c.CheckedAt)
}

func noticeLine(latest, cur, checkedAt string) string {
	if latest != "" && validVersion(latest) && compareVersions(latest, cur) > 0 {
		line := "[flint] new version v" + latest + " — flint update"
		if checkedAt != "" {
			line += " (checked " + checkedAt + ")"
		}
		return line
	}
	return ""
}

var updateRegistryURL = "https://raw.githubusercontent.com/synuns/flint/main/flint-plugin/.claude-plugin/plugin.json"

func registryCacheFile() string {
	return dataFile("registry-check.json")
}

type registryCache struct {
	Version   string `json:"version"`
	ETag      string `json:"etag,omitempty"`
	CheckedAt string `json:"checked_at"`
}

func fetchRegistryVersion() (string, error) {
	var c registryCache
	path := registryCacheFile()
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &c)
		}
	}
	client := updateClientFn(updateAPITimeout)
	req, err := http.NewRequest(http.MethodGet, updateRegistryURL, nil)
	if err != nil {
		return "", err
	}
	if c.ETag != "" {
		req.Header.Set("If-None-Match", c.ETag)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	etag := resp.Header.Get("ETag")
	switch resp.StatusCode {
	case http.StatusNotModified:

		if etag == "" {
			etag = c.ETag
		}
		if c.Version == "" {
			return "", errors.New("304 with no cached registry version")
		}
	case http.StatusOK:
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if rerr != nil {
			return "", rerr
		}
		var pj struct {
			Version string `json:"version"`
		}
		if jerr := json.Unmarshal(body, &pj); jerr != nil {
			return "", jerr
		}
		if pj.Version == "" {
			return "", errors.New("registry plugin.json missing version")
		}
		c.Version = pj.Version
	default:
		return "", fmt.Errorf("registry status %d", resp.StatusCode)
	}
	c.ETag, c.CheckedAt = etag, time.Now().UTC().Format(time.RFC3339)
	if path != "" {
		if b, merr := json.Marshal(c); merr == nil {
			_ = os.WriteFile(path, b, 0o600)
		}
	}
	return c.Version, nil
}

func updateCacheInfo() (latest, checkedAt string) {
	path := updateCacheFile()
	if path == "" {
		return "", ""
	}
	var c updateCache
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &c) == nil {
		return c.Latest, c.CheckedAt
	}
	return "", ""
}

func registryCacheInfo() (version, checkedAt string) {
	path := registryCacheFile()
	if path == "" {
		return "", ""
	}
	var c registryCache
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &c) == nil {
		return c.Version, c.CheckedAt
	}
	return "", ""
}

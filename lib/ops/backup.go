// the backup command — the CLI entry point of the ADR-0002 native SQL dump.
// `flint backup [--out file]` (stdout when unspecified) and `flint restore --file path`.
// Failures are a one-line JSON error + the exit-code contract (MapExit: 1 retry/general, 2 server down).
package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/synuns/flint/lib/backup"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// DumpFn and RestoreFn — seams. Real implementations by default; tests swap in fakes (SUPERLITE_REAL=1 delegates).
var (
	DumpFn        = backup.Dump
	RestoreFn     = backup.Restore
	DrillDataFn   = backup.RunDrillData
	RotateFn      = backup.Rotate
	KeepFn        = backup.BackupKeep
	EnsureKeyFn   = backup.EnsureIdentity
	EncryptToFn   = backup.EncryptTo
	MaskSecretsFn = store.MaskSecrets
)

// BackupKeyPath — the encryption backup identity path (the FLINT_HOME infrastructure root,
// with legacy auto-migration).
func BackupKeyPath() string {
	return DataFile("backup.key")
}

// buildMaskExtra — allowlist-registered values keep their raw form in the masked
// dump (plain text remains in shared dumps). With 1+ registrations, a warning joins extra.
func buildMaskExtra(allowlistN int) string {
	if allowlistN <= 0 {
		return ""
	}
	return fmt.Sprintf(`,"allowlist_entries":%d,"warn":"%d allowlist entries are not masked — re-review before sharing"`, allowlistN, allowlistN)
}

// dumpStore — full-dump access (a type-assertion seam in the same convention as approveStore).
// RunBackup dumps via the injected st (only an unimplemented st falls back to the
// DumpFn seam). The physical pool is opened and closed inside lib/backup.Dump (defer).
// The realStore delegation implementation is cli/realstore_supply.go (a cli-owned type).
type dumpStore interface {
	Dump(ctx context.Context, w io.Writer) error
}

// BackupEncryptOn — a delegation alias for the meta backup_encrypt verdict (the single
// source is wire.BackupEncryptOn — consistent across the manual, automatic, and doctor paths).
var BackupEncryptOn = wire.BackupEncryptOn

// RunBackup — produces the dump. The wire.Store parameter exists for root-dispatch
// compatibility and is unused (Dump opens its own pool). The stdout mode guarantees clean output via a buffer.
// --encrypt (age encryption, --out required) and --mask (secret-pattern substitution
// diagnostic dump — a non-restorable artifact without a sidecar).
// The argument-validation paths (unknown flag, missing value, --encrypt alone) are unified to
// invalid-arg exit 3.
func RunBackup(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// positional args not allowed (the wire.ParseArgs spec table).
	// Value validation (including typo guards) stays with the caller's contract as-is.
	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--out", Value: true},
			{Name: "--encrypt"},
			{Name: "--mask"},
		},
		Usage: "usage: flint backup [--out FILE] [--encrypt] [--mask]",
	})
	if perr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	out := res.Value("--out")
	// POSIX convention: the path "-" means stdout (same as unspecified) — without a preceding
	// ValidateFlagValue normalization, "-" would be rejected as a flag-shaped value.
	if out == "-" {
		out = ""
	}
	encrypt, mask := res.Given("--encrypt"), res.Given("--mask")
	if res.Given("--out") && !res.HasValue("--out") {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("--out requires a path — usage: flint backup [--out FILE] [--encrypt] [--mask]")))
		return 3
	}
	// a typo where the value absorbs a flag ('--out --encrypt' ->
	// the path "--encrypt") exits 3, the same contract as read --session.
	if err := knowledge.ValidateFlagValue("out", out); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	if encrypt && out == "" {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("--encrypt requires --out (ciphertext is file-only) — usage: flint backup [--out FILE] [--encrypt] [--mask]")))
		return 3
	}
	// v0.28.2: if --out is inside a git repository, warn (continue — preventing accidents
	// where an explicitly chosen or personal-data dump enters a repo and leaks via commit/sync).
	if out != "" && OutInsideGitRepo(out) {
		fmt.Fprintf(os.Stderr, "[flint] backup: --out path is inside a git repo — risk of committing or syncing the dump. Prefer ~/.flint/backup\n")
	}
	// with meta backup_encrypt=true, manual backups are also encrypted (consistency —
	// preventing a mix of plaintext without the flag and ciphertext for automatic ones). The
	// stdout pipe keeps the plaintext contract (encryption is file output only).
	if !encrypt && out != "" && backupEncryptMeta(ctx, st) {
		encrypt = true
	}

	var buf bytes.Buffer
	// the injected st takes precedence (realStore delegates Dump). Only an unimplemented st
	// falls back to the DumpFn seam (the existing test fakes) — only then is a self-connection opened.
	var dumpErr error
	if ds, ok := st.(dumpStore); ok {
		dumpErr = ds.Dump(ctx, &buf)
	} else {
		dumpErr = DumpFn(ctx, wire.DSNFromEnv(), &buf)
	}
	if dumpErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(dumpErr))
		return store.MapExit(dumpErr)
	}
	data := buf.Bytes()

	extra := ""
	if mask {
		// masking — for diagnostics and sharing: secret-pattern substitution + a non-restorable marker prefix.
		data = append([]byte(backup.MaskDumpHeader+"\n"), MaskSecretsFn(string(data))...)
		extra += `,"masked":true`
		extra += buildMaskExtra(store.SecretsAllowlistCount())
	}
	if encrypt {
		key := BackupKeyPath()
		if key == "" {
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("cannot determine config directory — cannot build key path")))
			return 1
		}
		id, err := EnsureKeyFn(key)
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return 1
		}
		var eb bytes.Buffer
		if err := EncryptToFn(&eb, data, id.Recipient()); err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return 1
		}
		data = eb.Bytes()
		extra += `,"encrypted":true`
	}

	if out == "" {
		// v0.28.0 (H3): a no-arg stdout dump exposes the whole DB in plaintext (terminal scrollback,
		// logs, agent context) — the posture conflict with the 0600 file policy is surfaced as a warning
		// (use is still allowed — the pipe-redirection convention).
		if !encrypt && !mask {
			fmt.Fprintln(os.Stderr, "[flint] backup: writing plaintext dump to stdout — beware terminal and log leaks (saving to a file is recommended)")
		}
		stdout.Write(data)
		return 0
	}
	if err := os.WriteFile(out, data, 0o600); err != nil { // v0.20.6 S3: sensitive plaintext dump
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	// v0.28.0 (H3): WriteFile's mode applies only to new files — reusing an existing
	// file leaves a relaxed mode, loosely exposing a sensitive dump. Forced.
	if err := os.Chmod(out, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "[flint] backup: failed to force mode 0600:", err)
	}
	// ADR-0006: create the original + .sha256 sidecar (for restore verification). Failure must not harm the dump's success.
	// An encrypted backup's sidecar is over the ciphertext (stored-artifact integrity —
	// plaintext integrity is guaranteed by age AEAD). A masked dump is a non-restorable
	// artifact, so it gets no sidecar (blocking the restore source + double defense with the marker).
	sumField := ""
	if !mask {
		if _, sum, cerr := backup.Checksum(bytes.NewBuffer(data)); cerr == nil {
			if serr := backup.WriteSidecar(out, sum); serr == nil {
				sumField = `,"sha256":` + QuoteJSON(sum)
			}
		}
	}
	// rotation at the end of the successful backup path — keep is meta backup_keep (default 14).
	// A rotation failure must not harm the backup's success either. QuoteJSON already wraps
	// quotes, so no extra `"` (doubled quotes corrupt the JSON).
	fmt.Fprintln(stdout, `{"status":"ok","path":`+QuoteJSON(out)+sumField+extra+rotateAfterBackup(ctx, st, out)+"}")
	return 0
}

// backupEncryptMeta — the meta backup_encrypt verdict (error/unset = false fallback).
// Meta access goes through the single wire.GetMetaKV entry (meta_kv.go).
func backupEncryptMeta(ctx context.Context, st wire.Store) bool {
	if st == nil {
		return false
	}
	v, ok := wire.GetMetaKV(ctx, st, "backup_encrypt")
	return ok && BackupEncryptOn(v)
}

// rotateAfterBackup — returns the rotation summary field (`,"removed":N`) to append after a
// successful --out path. Nothing deleted or a failure yields the empty string (failure is a warning only; exit unchanged).
func rotateAfterBackup(ctx context.Context, st wire.Store, out string) string {
	getKV := func(key string) (string, error) {
		if st == nil {
			return "", errors.New("no store")
		}
		// the lookup body delegates to the single wire.GetMetaKVErr entry — a missing key
		// ("", false, nil) is mapped to the existing unset case.
		v, ok, err := wire.GetMetaKVErr(ctx, st, key)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("meta %s not set", key)
		}
		return v, nil
	}
	removed, err := RotateFn(filepath.Dir(out), KeepFn(getKV))
	if err != nil {
		log.Printf("[flint] backup: rotate failed: %v", err)
		return ""
	}
	if len(removed) == 0 {
		return ""
	}
	return `,"removed":` + strconv.Itoa(len(removed))
}

// RunRestore — replays the dump script. --file is required. --drill, instead of a real replay,
// runs a replay drill against the temporary flint_drill_db database — the original is untouched; only the result is reported.
// The argument-validation paths (unknown flag, missing value, missing --file) are unified to
// invalid-arg exit 3.
func RunRestore(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// positional args not allowed (the wire.ParseArgs spec table).
	// Value validation (including typo guards) stays with the caller's contract as-is.
	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--drill"},
			{Name: "--file", Value: true},
		},
		Usage: "usage: flint restore --file FILE [--drill]",
	})
	if perr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	file := res.Value("--file")
	drill := res.Given("--drill")
	if res.Given("--file") && !res.HasValue("--file") {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("--file requires a path — usage: flint restore --file FILE [--drill]")))
		return 3
	}
	// the flag-absorption value guard (the same contract as --out).
	if err := knowledge.ValidateFlagValue("file", file); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	if file == "" {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("restore requires --file — usage: flint restore --file FILE [--drill]")))
		return 3
	}

	// the ADR-0006 restore guard — checksum verification before replay (the guard exists only here).
	// A backup file executes arbitrary SQL, so on tamper detection it aborts immediately.
	// v0.28.0 (H3): the file is read once and those bytes are both verified and replayed —
	// removing the TOCTOU window of reopening after verification (VerifyFile -> os.Open).
	raw, rerr2 := os.ReadFile(file)
	if rerr2 != nil {
		fmt.Fprintln(stdout, wire.ErrLine(rerr2))
		return 1
	}
	sum, verr := backup.VerifyBytes(raw, file+".sha256")
	if errors.Is(verr, backup.ErrChecksumMismatch) {
		fmt.Fprintln(stdout, mismatchLine(file))
		return 1
	}
	// a missing sidecar is also blocked before replay — a backup file is a
	// medium for arbitrary SQL, so replaying it without integrity verification violated ADR-0006's
	// 'abort immediately on tamper detection' intent (previously it proceeded with only a
	// post-exit warning; now it is a guard). The sidecar can be regenerated with `flint backup --out <same path>`.
	if errors.Is(verr, backup.ErrNoSidecar) {
		fmt.Fprintln(stdout, `{"error":"checksum sidecar missing — blocking replay without integrity verification (regenerate the backup: flint backup --out)"}`)
		return 1
	}
	if verr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(verr))
		return 1
	}
	_ = sum

	// a single read path — age decryption and mask rejection are applied to the verified
	// bytes. The key path is always passed (unused for plaintext).
	key := BackupKeyPath()
	data, rerr := backup.ReadDumpFrom(raw, key)
	if errors.Is(rerr, backup.ErrMaskedDump) {
		fmt.Fprintln(stdout, `{"error":"masked dump — masked dumps are for diagnostics and sharing only (not restorable)"}`)
		return 1
	}
	if errors.Is(rerr, backup.ErrKeyPathRequired) || errors.Is(rerr, backup.ErrEncryptKeyMissing) || errors.Is(rerr, backup.ErrEncryptKeyCorrupt) {
		fmt.Fprintf(stdout, `{"error":"cannot decrypt the encrypted backup — key file required or invalid (%s). A lost or damaged key cannot be recovered (RUNBOOK)"}`+"\n", key)
		return 1
	}
	if rerr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(rerr))
		return 1
	}

	// --drill — a drill instead of a real replay: replays into the flint_drill_db database and compares row counts.
	// The original is untouched; the result is one line of JSON (on mismatch, ok:false + exit 1).
	// The verified, decrypted bytes are replayed as-is (DrillDataFn).
	if drill {
		res, derr := DrillDataFn(wire.DSNFromEnv(), data)
		if derr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(derr))
			return store.MapExit(derr)
		}
		ok := "false"
		if res.Ok {
			ok = "true"
		}
		fmt.Fprintf(stdout, `{"status":"drill","expected":%d,"restored":%d,"ok":%s}`+"\n",
			res.Expected, res.Restored, ok)
		if !res.Ok {
			return 1
		}
		return 0
	}

	if err := RestoreFn(ctx, wire.DSNFromEnv(), bytes.NewReader(data)); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	// the one-line JSON result — a missing sidecar is blocked before replay,
	// so this is always the verification-passed branch.
	fmt.Fprintln(stdout, `{"status":"restored","sha256":`+QuoteJSON(sum)+"}")
	return 0
}

// mismatchLine — the checksum-mismatch warning as one-line JSON (including the arbitrary-SQL-execution-blocking notice).
func mismatchLine(path string) string {
	m := map[string]string{
		"error":   "checksum_mismatch",
		"message": "backup files run arbitrary SQL — checksum tampering detected",
		"path":    path,
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// QuoteJSON — wraps a path or other string as a JSON string literal.
// An alias of the single wire.QuoteJSON implementation (keeping the export for cli-consumer compatibility).
var QuoteJSON = wire.QuoteJSON

// OutInsideGitRepo — determines whether a path is inside a git repository (work tree).
// Recognizes both a .git directory (normal) and a file (worktree), walking up at most 64 levels.
func OutInsideGitRepo(out string) bool {
	dir, err := filepath.Abs(filepath.Dir(out))
	if err != nil {
		return false
	}
	for i := 0; i < 64; i++ {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
	return false
}

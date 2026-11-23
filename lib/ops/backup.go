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

func BackupKeyPath() string {
	return DataFile("backup.key")
}

func buildMaskExtra(allowlistN int) string {
	if allowlistN <= 0 {
		return ""
	}
	return fmt.Sprintf(`,"allowlist_entries":%d,"warn":"%d allowlist entries are not masked — re-review before sharing"`, allowlistN, allowlistN)
}

type dumpStore interface {
	Dump(ctx context.Context, w io.Writer) error
}

var BackupEncryptOn = wire.BackupEncryptOn

func RunBackup(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {

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

	if out == "-" {
		out = ""
	}
	encrypt, mask := res.Given("--encrypt"), res.Given("--mask")
	if res.Given("--out") && !res.HasValue("--out") {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("--out requires a path — usage: flint backup [--out FILE] [--encrypt] [--mask]")))
		return 3
	}

	if err := knowledge.ValidateFlagValue("out", out); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	if encrypt && out == "" {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("--encrypt requires --out (ciphertext is file-only) — usage: flint backup [--out FILE] [--encrypt] [--mask]")))
		return 3
	}

	if out != "" && OutInsideGitRepo(out) {
		fmt.Fprintf(os.Stderr, "[flint] backup: --out path is inside a git repo — risk of committing or syncing the dump. Prefer ~/.flint/backup\n")
	}

	if !encrypt && out != "" && backupEncryptMeta(ctx, st) {
		encrypt = true
	}

	var buf bytes.Buffer

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

		if !encrypt && !mask {
			fmt.Fprintln(os.Stderr, "[flint] backup: writing plaintext dump to stdout — beware terminal and log leaks (saving to a file is recommended)")
		}
		stdout.Write(data)
		return 0
	}
	if err := os.WriteFile(out, data, 0o600); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}

	if err := os.Chmod(out, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "[flint] backup: failed to force mode 0600:", err)
	}

	sumField := ""
	if !mask {
		if _, sum, cerr := backup.Checksum(bytes.NewBuffer(data)); cerr == nil {
			if serr := backup.WriteSidecar(out, sum); serr == nil {
				sumField = `,"sha256":` + QuoteJSON(sum)
			}
		}
	}

	fmt.Fprintln(stdout, `{"status":"ok","path":`+QuoteJSON(out)+sumField+extra+rotateAfterBackup(ctx, st, out)+"}")
	return 0
}

func backupEncryptMeta(ctx context.Context, st wire.Store) bool {
	if st == nil {
		return false
	}
	v, ok := wire.GetMetaKV(ctx, st, "backup_encrypt")
	return ok && BackupEncryptOn(v)
}

func rotateAfterBackup(ctx context.Context, st wire.Store, out string) string {
	getKV := func(key string) (string, error) {
		if st == nil {
			return "", errors.New("no store")
		}

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

func RunRestore(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {

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

	if err := knowledge.ValidateFlagValue("file", file); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	if file == "" {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("restore requires --file — usage: flint restore --file FILE [--drill]")))
		return 3
	}

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

	if errors.Is(verr, backup.ErrNoSidecar) {
		fmt.Fprintln(stdout, `{"error":"checksum sidecar missing — blocking replay without integrity verification (regenerate the backup: flint backup --out)"}`)
		return 1
	}
	if verr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(verr))
		return 1
	}
	_ = sum

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

	fmt.Fprintln(stdout, `{"status":"restored","sha256":`+QuoteJSON(sum)+"}")
	return 0
}

func mismatchLine(path string) string {
	m := map[string]string{
		"error":   "checksum_mismatch",
		"message": "backup files run arbitrary SQL — checksum tampering detected",
		"path":    path,
	}
	b, _ := json.Marshal(m)
	return string(b)
}

var QuoteJSON = wire.QuoteJSON

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

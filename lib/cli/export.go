// export.go — flint export CLI wiring. The source query (ExportRows) and
// the 4 formatters (jsonl, md, txt, csv) consume the knowledge source via seam vars
// (for test fake injection and as transitional unconnected defense). Output contract: the body goes to
// stdout (default) or the --out file; the summary and overwrite warning are one stderr line each.
package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/synuns/flint/lib/backup"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/tui"
	"github.com/synuns/flint/lib/wire"
)

// TEMP done — ExportRow, ExportFilter, the source, and the formatters all connect to the knowledge
// source. The seams remain as vars for test fake injection (return-shape unifying
// wrappers — FormatMD and FormatTXT return a single value). SUPERLITE_REAL=1 means this default
// real-implementation path is itself the live-connection verification.
var (
	exportRowsFn = knowledge.ExportRows
	// exportLinksFn — the knowledge_links source of the PAM format
	// (v1.20.0 T5): the queried rows ride the FormatPAM links variadic
	// (absent links = the legacy supersedes-derived document,
	// byte-identical).
	exportLinksFn = knowledge.ExportLinkRows
	// formatter seams return (body, summary-reported row count, error).
	// jsonl, md, txt, and csv report the total row count (len(rows)); pam reports the number of published documents
	// (total_memories — pending excluded by pam design).
	formatJSONLFn = func(rows []knowledge.ExportRow) ([]byte, int, error) {
		b, err := knowledge.FormatJSONL(rows)
		return b, len(rows), err
	}
	// v1.20.0 T5 wiring (T4 consumer): the chains variadic carries the
	// supersede_chains view rows (empty = the legacy document,
	// byte-identical).
	formatMDFn = func(rows []knowledge.ExportRow, chains ...map[int][]int) ([]byte, int, error) {
		if len(chains) > 0 {
			return knowledge.FormatMD(rows, chains[0]), len(rows), nil
		}
		return knowledge.FormatMD(rows), len(rows), nil
	}
	formatTXTFn = func(rows []knowledge.ExportRow) ([]byte, int, error) {
		return knowledge.FormatTXT(rows), len(rows), nil
	}
	formatCSVFn = func(rows []knowledge.ExportRow) ([]byte, int, error) {
		b, err := knowledge.FormatCSV(rows)
		return b, len(rows), err
	}
	// PAM v1.0 exchange document — exported_by is "flint/"+version
	// (the CurrentVersion convention: ldflags -> buildinfo -> dev fallback).
	// The published count (total_memories) comes straight from the formatter.
	// v1.20.0 T5: the links variadic carries the knowledge_links rows
	// (empty = the legacy document, byte-identical).
	formatPAMFn = func(rows []knowledge.ExportRow, links ...knowledge.PAMLinkRow) ([]byte, int, error) {
		return knowledge.FormatPAMWithCount(rows, "flint/"+CurrentVersion(), links...)
	}
	// §18: the `--format pam --sign` signing path — the key is
	// knowledge.LoadOrCreatePAMSigningKey(configDir()/pam-signing-key).
	// returning the published count via the same route as formatPAMFn.
	formatPAMSignedFn = func(rows []knowledge.ExportRow, key ed25519.PrivateKey, links ...knowledge.PAMLinkRow) ([]byte, int, error) {
		return knowledge.FormatPAMSignedWithCount(rows, "flint/"+CurrentVersion(), key, links...)
	}
)

// exportSignKeyFn — §18 signing-key load seam (test fake injection).
var exportSignKeyFn = knowledge.LoadOrCreatePAMSigningKey

// exportStderr — summary/warning channel seam (the stderr contract — separate from the stdout body).
var exportStderr io.Writer = os.Stderr

// exportTUIFn — delegation seam for the `--tui` entry branch. Default is
// tui.RunExport (form -> preview -> arg assembly -> re-run runExport) — tests inject a fake
// to verify only the entry branch (the form flow itself is lib/tui/export_test.go).
var exportTUIFn = tui.RunExport

// exportTTYFn — TTY check seam for adaptive entry (real implementation
// tui.IsTerminalStdout by default; tests swap in a fake).
var exportTTYFn = tui.IsTerminalStdout

// exportFormatters — format name -> formatter seam lookup (also used to detect invalid formats).
// The returned row count is the summary-report basis (pam: published count).
func exportFormatters() map[string]func([]knowledge.ExportRow) ([]byte, int, error) {
	return map[string]func([]knowledge.ExportRow) ([]byte, int, error){
		"jsonl": formatJSONLFn,
		"md":    func(rows []knowledge.ExportRow) ([]byte, int, error) { return formatMDFn(rows) },
		"txt":   formatTXTFn,
		"csv":   formatCSVFn,
		"pam":   func(rows []knowledge.ExportRow) ([]byte, int, error) { return formatPAMFn(rows) },
	}
}

func init() { Register("export", runExport) }

// runExport — `flint export [--format jsonl|md|txt|csv|pam] [--out FILE]
// [--status active|all] [--tag X] [--since RFC3339] [--sign (pam only —
// §18 signing)]`. Defaults: jsonl to stdout.
// Invalid format, status, since, or unknown flag exits 3; nil store or server-down exits
// 2 (reusing MapExit). On success, one summary line on stderr:
// `exported N rows (format, M bytes)`.
func runExport(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// entry branch + adaptive switching: explicit --tui or
	// no args or flags + terminal delegates to the form flow without parsing flags
	// (the form re-enters runExport with assembled args — on re-entry args exist, so no adaptive
	// re-divergence). The flag path is unchanged.
	tuiEntry := false
	for _, a := range args {
		if a == "--tui" {
			tuiEntry = true
			break
		}
	}
	// layer 1 of the 3-layer guard — with FLINT_NO_TUI set, blocked regardless of TTY.
	if !tuiEntry && len(args) == 0 && !tui.NoTUIForced() && exportTTYFn() {
		tuiEntry = true
	}
	if tuiEntry {
		return runExportTUI(ctx, st, stdin, stdout)
	}
	// moved the manual loop to the wire.ParseArgs spec table (positional args
	// not allowed — carrying over the read-family consumption convention: always consume a value, a missing trailing value is
	// an empty-value check by the caller exiting 3). Value validation reproduces occurrence order (AllOccs).
	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--format", Value: true},
			{Name: "--out", Value: true},
			{Name: "--status", Value: true},
			{Name: "--tag", Value: true},
			{Name: "--since", Value: true},
			// §18 — pam-only signing flag (consumes no value).
			{Name: "--sign"},
		},
		Usage: "usage: flint export [--format jsonl|md|txt|csv|pam] [--out FILE] [--status active|all] [--tag X] [--since RFC3339] [--sign (pam only)]",
	})
	if perr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	format, outPath, status, tag := "jsonl", "", "active", ""
	sign := res.Given("--sign")
	var since time.Time
	for _, o := range res.AllOccs() {
		switch o.Name {
		case "--format":
			if o.Value == "" || !validExportFormat(o.Value) {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "format", Expect: "jsonl|md|txt|csv|pam"}))
				return 3
			}
			format = o.Value
		case "--out":
			if o.Value == "" {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "out", Expect: "non-empty"}))
				return 3
			}
			// POSIX convention: the path "-" means stdout (same as unspecified).
			if o.Value == "-" {
				outPath = ""
			} else {
				outPath = o.Value
			}
		case "--status":
			if o.Value != "active" && o.Value != "all" {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "status", Expect: "active|all"}))
				return 3
			}
			status = o.Value
		case "--tag":
			// carrying over the read convention: on repeated specification, the last value wins.
			if o.Value == "" {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "tag", Expect: "non-empty"}))
				return 3
			}
			tag = o.Value
		case "--since":
			t, err := time.Parse(time.RFC3339, o.Value)
			if err != nil {
				fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "since", Expect: "RFC3339"}))
				return 3
			}
			since = t
		}
	}
	// --sign is exclusive to the PAM §18 signing path — combining with other formats is a usage error.
	if sign && format != "pam" {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "sign", Expect: "requires --format pam"}))
		return 3
	}
	// contract: nil store or server-down exits 2. export is not store-free, so
	// nil is treated the same as server-down (one ErrLine + MapExit).
	if st == nil {
		fmt.Fprintln(stdout, wire.ErrLine(store.ErrServerDown))
		return store.MapExit(store.ErrServerDown)
	}
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if exportRowsFn == nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("export: source not linked")))
		return 1
	}
	rows, err := exportRowsFn(ctx, st, knowledge.ExportFilter{Status: status, Tag: tag, Since: since})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	// cap on the query result row count (registry key export_import_max_rows —
	// shared with import; a memory/output defense for large exports). The check runs right after the query and before
	// formatting, so it does not precede full buffering, but it blocks formatter and write side
	// effects.
	if code := enforceExportMaxRows(ctx, st, stdout, len(rows)); code != 0 {
		return code
	}
	fn := exportFormatters()[format]
	if fn == nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("export: formatter %s not linked", format)))
		return 1
	}
	// v1.20.0 T5 wiring (T4 consumer) — the md format additionally
	// carries the supersede chain notation (the supersede_chains view
	// via SupersedeChains; an empty/absent map keeps the document
	// byte-identical to the legacy one).
	if format == "md" {
		chains, cerr := knowledge.SupersedeChains(ctx, st)
		if cerr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(cerr))
			return store.MapExit(cerr)
		}
		mfn := formatMDFn
		fn = func(rows []knowledge.ExportRow) ([]byte, int, error) {
			return mfn(rows, chains)
		}
	}
	// v1.20.0 T5 — the pam format additionally carries the
	// knowledge_links relations (queried here once; the formatter's
	// links variadic keeps an absent/empty list byte-identical to the
	// legacy supersedes-derived document).
	var pamLinks []knowledge.PAMLinkRow
	if format == "pam" {
		var lerr error
		pamLinks, lerr = exportLinksFn(ctx, st)
		if lerr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(lerr))
			return store.MapExit(lerr)
		}
		pfn := formatPAMFn
		fn = func(rows []knowledge.ExportRow) ([]byte, int, error) {
			return pfn(rows, pamLinks...)
		}
	}
	if sign {
		// §18: load the signing key (creating it if absent — 0600), then swap in the signing formatter.
		key, kerr := exportSignKeyFn()
		if kerr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("export: pam signing key unavailable: %v", kerr)))
			return 1
		}
		sfn := formatPAMSignedFn
		fn = func(rows []knowledge.ExportRow) ([]byte, int, error) { return sfn(rows, key, pamLinks...) }
	}
	data, summaryRows, err := fn(rows)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	if outPath != "" {
		// overwriting an existing file — one warning line to stderr before writing.
		if _, serr := os.Stat(outPath); serr == nil {
			fmt.Fprintf(exportStderr, "export: overwriting existing file: %s\n", outPath)
		}
		if werr := os.WriteFile(outPath, data, 0o644); werr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(werr))
			return 1
		}
		// on successful write, a .sha256 sidecar (carrying over the backup --out
		// convention — format unified via backup.WriteSidecar, sha256sum -c compatible).
		// failure is a stderr warning only: it must not harm the successful body write (ADR-0006).
		digest := sha256.Sum256(data)
		if serr := backup.WriteSidecar(outPath, hex.EncodeToString(digest[:])); serr != nil {
			fmt.Fprintf(exportStderr, "export: checksum sidecar write failed: %v\n", serr)
		}
	} else if _, werr := stdout.Write(data); werr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(werr))
		return 1
	}
	// the summary row count is the formatter-reported value — pam reports published documents (pending
	// excluded); the other formats report total rows.
	fmt.Fprintf(exportStderr, "exported %d rows (%s, %d bytes)\n", summaryRows, format, len(data))
	return 0
}

// runExportTUI — wiring for `flint export --tui`. The form and confirmation go to
// tui.RunExport; the query, formatters, cap, and execution are injected by wrapping the existing seams (exportRowsFn,
// exportFormatters, exportMaxRowsLimit, runExport) —
// the TUI does not reimplement source logic (sidecar, cap, and summary apply automatically via the
// runExport re-run). Errors (including non-terminal) are one ErrLine + exit 3.
func runExportTUI(ctx context.Context, st Store, stdin io.Reader, stdout io.Writer) int {
	code, err := exportTUIFn(ctx, tui.ExportDeps{
		Preview: func(ctx context.Context, sel tui.ExportSelection) (tui.ExportPreview, error) {
			if exportRowsFn == nil {
				return tui.ExportPreview{}, fmt.Errorf("export: source not linked")
			}
			rows, err := exportRowsFn(ctx, st, knowledge.ExportFilter{Status: sel.Status})
			if err != nil {
				return tui.ExportPreview{}, err
			}
			fn := exportFormatters()[sel.Format]
			if fn == nil {
				return tui.ExportPreview{}, fmt.Errorf("export: formatter %s not linked", sel.Format)
			}
			data, _, err := fn(rows)
			if err != nil {
				return tui.ExportPreview{}, err
			}
			return tui.ExportPreview{Rows: len(rows), Bytes: len(data)}, nil
		},
		MaxRows: func(ctx context.Context) int { return exportMaxRowsLimit(ctx, st) },
		Run:     func(assembled []string) int { return runExport(ctx, st, assembled, stdin, stdout) },
	})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	return code
}

// exportMaxRowsLimit — loads the per-export row cap (registry key
// export_import_max_rows — shared with import). Query failure or unset (0) falls back to
// DefaultThresholds (same convention as importMaxRowsLimit).
func exportMaxRowsLimit(ctx context.Context, st Store) int {
	def := store.DefaultThresholds().ExportImportMaxRows
	th, err := wire.LoadThresholdsFn(ctx, st)
	if err != nil || th.ExportImportMaxRows <= 0 {
		return def
	}
	return th.ExportImportMaxRows
}

// enforceExportMaxRows — on exceeding the row cap, prints an error JSON (including max and count) to
// stdout and returns 3 (the caller returns it as-is). The format follows import's
// enforceImportMaxRows. It runs before the formatters and file write, so no side effects.
func enforceExportMaxRows(ctx context.Context, st Store, stdout io.Writer, count int) int {
	max := exportMaxRowsLimit(ctx, st)
	if count <= max {
		return 0
	}
	b, err := json.Marshal(map[string]any{
		"error": fmt.Sprintf("export row limit exceeded (export_import_max_rows=%d)", max),
		"max":   max,
		"count": count,
	})
	if err != nil {
		b = []byte(fmt.Sprintf(`{"error":"export row limit exceeded","max":%d,"count":%d}`, max, count))
	}
	fmt.Fprintln(stdout, string(b))
	return 3
}

// validExportFormat — for flag-validation time (unconnected-formatter detection happens at query time).
func validExportFormat(v string) bool {
	switch v {
	case "jsonl", "md", "txt", "csv", "pam":
		return true
	}
	return false
}

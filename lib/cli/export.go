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

var (
	exportRowsFn = knowledge.ExportRows

	exportLinksFn = knowledge.ExportLinkRows

	formatJSONLFn = func(rows []knowledge.ExportRow) ([]byte, int, error) {
		b, err := knowledge.FormatJSONL(rows)
		return b, len(rows), err
	}

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

	formatPAMFn = func(rows []knowledge.ExportRow, links ...knowledge.PAMLinkRow) ([]byte, int, error) {
		return knowledge.FormatPAMWithCount(rows, "flint/"+CurrentVersion(), links...)
	}

	formatPAMSignedFn = func(rows []knowledge.ExportRow, key ed25519.PrivateKey, links ...knowledge.PAMLinkRow) ([]byte, int, error) {
		return knowledge.FormatPAMSignedWithCount(rows, "flint/"+CurrentVersion(), key, links...)
	}
)

var exportSignKeyFn = knowledge.LoadOrCreatePAMSigningKey

var exportStderr io.Writer = os.Stderr

var exportTUIFn = tui.RunExport

var exportTTYFn = tui.IsTerminalStdout

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

func runExport(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {

	tuiEntry := false
	for _, a := range args {
		if a == "--tui" {
			tuiEntry = true
			break
		}
	}

	if !tuiEntry && len(args) == 0 && !tui.NoTUIForced() && exportTTYFn() {
		tuiEntry = true
	}
	if tuiEntry {
		return runExportTUI(ctx, st, stdin, stdout)
	}

	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--format", Value: true},
			{Name: "--out", Value: true},
			{Name: "--status", Value: true},
			{Name: "--tag", Value: true},
			{Name: "--since", Value: true},

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

	if sign && format != "pam" {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "sign", Expect: "requires --format pam"}))
		return 3
	}

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

	if code := enforceExportMaxRows(ctx, st, stdout, len(rows)); code != 0 {
		return code
	}
	fn := exportFormatters()[format]
	if fn == nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("export: formatter %s not linked", format)))
		return 1
	}

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

		if _, serr := os.Stat(outPath); serr == nil {
			fmt.Fprintf(exportStderr, "export: overwriting existing file: %s\n", outPath)
		}
		if werr := os.WriteFile(outPath, data, 0o644); werr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(werr))
			return 1
		}

		digest := sha256.Sum256(data)
		if serr := backup.WriteSidecar(outPath, hex.EncodeToString(digest[:])); serr != nil {
			fmt.Fprintf(exportStderr, "export: checksum sidecar write failed: %v\n", serr)
		}
	} else if _, werr := stdout.Write(data); werr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(werr))
		return 1
	}

	fmt.Fprintf(exportStderr, "exported %d rows (%s, %d bytes)\n", summaryRows, format, len(data))
	return 0
}

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

func exportMaxRowsLimit(ctx context.Context, st Store) int {
	def := store.DefaultThresholds().ExportImportMaxRows
	th, err := wire.LoadThresholdsFn(ctx, st)
	if err != nil || th.ExportImportMaxRows <= 0 {
		return def
	}
	return th.ExportImportMaxRows
}

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

func validExportFormat(v string) bool {
	switch v {
	case "jsonl", "md", "txt", "csv", "pam":
		return true
	}
	return false
}

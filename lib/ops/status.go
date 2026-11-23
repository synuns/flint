package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

type DailyRow struct {
	Read     int
	ReadZero int
	ReadNear int
}

var WindowedDailyFn func(ctx context.Context, st wire.Store, days int) ([]DailyRow, error)

var ReadZeroPctFn func(calls, zero int) int

var HotStatsFn func() (calls int, p95 int, max int)

var LoadHotStatsFromMetaFn func(ctx context.Context, st wire.Store)

var LoadReadStatsFromMetaFn func(ctx context.Context, st wire.Store) (calls int, zero int)

type StatusOutput struct {
	Status        string  `json:"status"`
	SchemaVersion string  `json:"schema_version"`
	Pending       int     `json:"pending"`
	HotCalls      int     `json:"hot_calls"`
	HotP95        int     `json:"hot_p95"`
	ReadCalls     int     `json:"read_calls"`
	ReadZeroPct   int     `json:"read_zero_pct"`
	LastWriteAt   *string `json:"last_write_at"`
	LastHotAt     *string `json:"last_hot_at"`
	LastReadAt    *string `json:"last_read_at"`
}

type statusV096Output struct {
	StatusOutput
	ReadFallbackCalls *int `json:"read_fallback_calls"`
}

type statusV0180Output struct {
	statusV096Output
	ReadCalls14d       int `json:"read_calls_14d"`
	ReadZeroPct14d     int `json:"read_zero_pct_14d"`
	ReadZeroNearPct14d int `json:"read_zero_near_pct_14d"`

	ReadWindowDays int `json:"read_window_days"`

	HotBytes14d  int `json:"hot_bytes_14d"`
	ReadBytes14d int `json:"read_bytes_14d"`

	ReadBytesP95 int `json:"read_bytes_p95"`
	HotBytesP95  int `json:"hot_bytes_p95"`
}

func StatusWindowedRead(ctx context.Context, st wire.Store, days int) (read, zeroPct, nearPct int) {
	dayRows, err := WindowedDailyFn(ctx, st, days)
	if err != nil {
		return 0, 0, 0
	}
	var zero, near int
	for _, d := range dayRows {
		read += d.Read
		zero += d.ReadZero
		near += d.ReadNear
	}
	return read, ReadZeroPctFn(read, zero), ReadZeroPctFn(read, near)
}

func StatusWindowedBytes(ctx context.Context, st wire.Store, days int) (hotBytes, readBytes int) {
	rows, err := WindowedDaily(ctx, st, days)
	if err != nil {
		return 0, 0
	}
	for _, d := range rows {
		hotBytes += d.HotBytes
		readBytes += d.ReadBytes
	}
	return hotBytes, readBytes
}

type statusV1210Output struct {
	statusV0180Output
	VaultIndexedAt *string `json:"vault_indexed_at"`
	VaultFiles     int     `json:"vault_files"`
	VaultChanged   int     `json:"vault_changed"`
}

type statusV1211Output struct {
	statusV1210Output
	ScanEchoCalls    int `json:"scan_echo_calls"`
	ScanEchoP95Us    int `json:"scan_echo_p95_us"`
	ScanSuggestCalls int `json:"scan_suggest_calls"`
	ScanSuggestP95Us int `json:"scan_suggest_p95_us"`
}

type statusV1250Output struct {
	statusV1211Output
	EngineMemMB int `json:"engine_mem_mb,omitempty"`
}

func vaultLastScanRow(getKV func(string, string) (string, error)) (*string, int, int) {
	v, err := getKV("vault_last_scan", "")
	if err != nil || v == "" {
		return nil, 0, 0
	}
	var ls struct {
		At      string `json:"at"`
		Files   int    `json:"files"`
		Changed int    `json:"changed"`
	}
	if json.Unmarshal([]byte(v), &ls) != nil {
		return nil, 0, 0
	}
	if _, perr := time.Parse(time.RFC3339, strings.TrimSpace(ls.At)); perr != nil {
		ls.At = ""
	}
	if ls.At == "" {
		return nil, ls.Files, ls.Changed
	}
	at := strings.TrimSpace(ls.At)
	return &at, ls.Files, ls.Changed
}

type statusAsOfOutput struct {
	statusV0180Output
	AsOf knowledge.AsOfInfo `json:"as_of"`
}

func FallbackCallsOrNull(getKV func(string, string) (string, error)) *int {
	v, err := getKV("read_fallback_calls", "")
	if err != nil || v == "" {
		return nil
	}
	n, perr := strconv.Atoi(strings.TrimSpace(v))
	if perr != nil {
		return nil
	}
	return &n
}

func lastSeenOrNull(getKV func(string, string) (string, error), key string) *string {
	v, err := getKV(key, "")
	if err != nil || v == "" {
		return nil
	}
	if _, perr := time.Parse(time.RFC3339, strings.TrimSpace(v)); perr != nil {
		return nil
	}
	s := strings.TrimSpace(v)
	return &s
}

func lastReadAtOrNull(ctx context.Context, st wire.Store, getKV func(string, string) (string, error)) *string {
	out := lastSeenOrNull(getKV, "last_read_at")
	rows, err := wire.QueryOnce(ctx, st, `SELECT max(ts) FROM read_events`, nil)
	if err == nil && len(rows) > 0 && len(rows[0]) > 0 && rows[0][0] != nil {
		if t, ok := CellToTime(rows[0][0]); ok {
			s := t.UTC().Format(time.RFC3339)
			if out == nil {
				out = &s
			} else if prev, perr := time.Parse(time.RFC3339, *out); perr == nil && t.After(prev) {
				out = &s
			}
		}
	}
	return out
}

func RunStatus(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	asOfSpec := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--as-of" {
			if i+1 >= len(args) {

				fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("empty as-of spec: %w", ErrInvalidAsOf)))
				return 3
			}
			asOfSpec, i = args[i+1], i+1
		}
	}
	asOfHash, asOfDate := "", ""
	if asOfSpec != "" {
		hash, err := ParseAsOfSpecFn(ctx, st, asOfSpec)
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			if errors.Is(err, ErrInvalidAsOf) {
				return 3
			}
			return store.MapExit(err)
		}
		asOfHash = hash

		if entries, err := st.LogEntries(ctx, wire.LogQuery{Hash: hash}); err == nil && len(entries) > 0 {
			asOfDate = knowledge.FmtAsOfDate(entries[0].Date)
		}
	}
	rows, err := wire.QueryOnce(ctx, st, `SELECT value FROM meta WHERE key = 'schema_version'`, nil)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	version := ""
	if len(rows) > 0 && rows[0][0] != nil {
		version = fmt.Sprint(rows[0][0])
	}

	ver, perr := strconv.Atoi(strings.TrimSpace(version))
	if perr != nil || ver < 1 || ver > db.LatestSchemaVersion {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "schema_version",
			Expect: "1~" + strconv.Itoa(db.LatestSchemaVersion)}))
		return 3
	}

	winDays := knowledge.WriteThresholds(ctx, st).DailyRetentionD
	winRead, winZeroPct, winNearPct := StatusWindowedRead(ctx, st, winDays)

	winHotBytes, winReadBytes := StatusWindowedBytes(ctx, st, winDays)

	readBytesP95, hotBytesP95 := 0, 0
	if asOfSpec == "" {
		readBytesP95 = ReadBytesP95Of(ctx, st)
		_, hotBytesP95 = DerivedHotStats(ctx, st)
	}

	pending := 0
	if asOfHash != "" {

		if !knowledge.ValidAsOfHash(asOfHash) {
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("as-of hash has invalid characters: %q", asOfHash)))
			return 1
		}
		asOfRows, err := st.QueryAsOf(ctx, wire.AsOfQuery{Hash: asOfHash, Table: "knowledge",
			Columns: []string{"count(*)"}, Where: "status = 'pending' AND invalid_at IS NULL"})
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
		if len(asOfRows) > 0 {
			fmt.Sscan(fmt.Sprint(asOfRows[0][0]), &pending)
		}
	} else {
		n, cerr := knowledge.CountPending(ctx, st)
		if cerr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(cerr))
			return store.MapExit(cerr)
		}
		pending = n
	}

	LoadHotStatsFromMetaFn(ctx, st)
	calls, p95, _ := HotStatsFn()
	readCalls, readZero := LoadReadStatsFromMetaFn(ctx, st)

	getKV := knowledge.MetaGetKV(ctx, st)
	base := statusV096Output{StatusOutput: StatusOutput{Status: "ok", SchemaVersion: version, Pending: pending,
		HotCalls: calls, HotP95: p95, ReadCalls: readCalls, ReadZeroPct: ReadZeroPctFn(readCalls, readZero),
		LastWriteAt: lastSeenOrNull(getKV, "last_write_at"),
		LastHotAt:   lastSeenOrNull(getKV, "last_hot_at"),

		LastReadAt: lastReadAtOrNull(ctx, st, getKV)},
		ReadFallbackCalls: FallbackCallsOrNull(getKV)}
	var b []byte
	if asOfHash != "" {
		b, _ = json.Marshal(statusAsOfOutput{statusV0180Output: statusV0180Output{statusV096Output: base,
			ReadCalls14d: winRead, ReadZeroPct14d: winZeroPct, ReadZeroNearPct14d: winNearPct, ReadWindowDays: winDays,
			HotBytes14d: winHotBytes, ReadBytes14d: winReadBytes,
			ReadBytesP95: readBytesP95, HotBytesP95: hotBytesP95},
			AsOf: knowledge.AsOfInfo{Hash: asOfHash, CommitDate: asOfDate}})
	} else {

		vAt, vFiles, vChanged := vaultLastScanRow(getKV)

		sEcho, sSuggest := ScanStatsFromMetaFn(func(k string) (string, error) { return getKV(k, "") })

		engineMem, _, _ := EngineMemMB(ctx)
		b, _ = json.Marshal(statusV1250Output{statusV1211Output: statusV1211Output{statusV1210Output: statusV1210Output{statusV0180Output: statusV0180Output{statusV096Output: base,
			ReadCalls14d: winRead, ReadZeroPct14d: winZeroPct, ReadZeroNearPct14d: winNearPct, ReadWindowDays: winDays,
			HotBytes14d: winHotBytes, ReadBytes14d: winReadBytes,
			ReadBytesP95: readBytesP95, HotBytesP95: hotBytesP95},
			VaultIndexedAt: vAt, VaultFiles: vFiles, VaultChanged: vChanged},
			ScanEchoCalls: sEcho.Calls, ScanEchoP95Us: sEcho.P95,
			ScanSuggestCalls: sSuggest.Calls, ScanSuggestP95Us: sSuggest.P95},
			EngineMemMB: engineMem})
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

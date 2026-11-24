package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"sort"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/i18n"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

var (
	CurrentVersionFn     func() string
	FetchLatestVersionFn func() (string, error)
	ValidVersionFn       func(v string) bool
	CompareVersionsFn    func(a, b string) int

	UpdateCacheInfoFn func() (string, string)

	FetchRegistryVersionFn func() (string, error)

	RegistryCacheInfoFn func() (string, string)
	EngineModeFn        func() (string, error)

	KnownMetaKeyFn func(k string) bool

	AnyToTimeFn func(v any) (time.Time, bool)

	IsConflictGroupFn      func(g db.ConflictGroup, dupThreshold float64) bool
	ConflictDupThresholdFn func() float64

	BackupEncryptOnFn func(v string) bool
	BackupKeyPathFn   func() string

	SkillSrcDirFn    func(binPath string) string
	CheckSkillFileFn func(srcDir, destDir string) (DoctorCheck, bool)
)

type CheckSpec struct {
	Name string
	Fn   func(ctx context.Context, st wire.Store) DoctorCheck

	ThFn func(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck

	Chain string

	OnStoreNil string
}

func doctorChecks() []CheckSpec {
	return []CheckSpec{
		{Name: "hook-file", Chain: "core", OnStoreNil: "",
			Fn: func(ctx context.Context, st wire.Store) DoctorCheck { return checkHookFile() }},
		{Name: "db", Chain: "core", OnStoreNil: "fail", Fn: checkDB},
		{Name: "schema", Chain: "core", OnStoreNil: "fail", Fn: checkSchema},
		{Name: "integrity", Chain: "core", OnStoreNil: "fail", Fn: checkIntegrity},
		{Name: "last-seen", Chain: "core", OnStoreNil: "fail", ThFn: checkLastSeenTh},
		{Name: "port", Chain: "core", OnStoreNil: "",
			Fn: func(ctx context.Context, st wire.Store) DoctorCheck { return checkPort(ctx) }},
		{Name: "backup-age", Chain: "tail", OnStoreNil: "warn", ThFn: checkBackupAgeTh},
		{Name: "conflicts", Chain: "tail", OnStoreNil: "warn", Fn: CheckConflicts},
		{Name: "zero-near", Chain: "tail", OnStoreNil: "warn", ThFn: checkZeroNearTh},
		{Name: "pending-age", Chain: "tail", OnStoreNil: "warn", ThFn: checkPendingAgeTh},
		{Name: "event-volume", Chain: "tail", OnStoreNil: "warn", ThFn: checkEventVolumeTh},
		{Name: "backup-key", Chain: "tail", OnStoreNil: "warn", Fn: checkBackupKey},
		{Name: "meta-hygiene", Chain: "tail", OnStoreNil: "warn", ThFn: checkMetaHygieneTh},
		{Name: "update", Chain: "tail", OnStoreNil: "", Fn: checkUpdate},
		{Name: "recall-starved", Chain: "tail", OnStoreNil: "warn", Fn: checkRecallStarved},
		{Name: "metering-freshness", Chain: "tail", OnStoreNil: "warn", ThFn: checkMeteringFreshnessTh},
		{Name: "secrets", Chain: "tail", OnStoreNil: "warn", ThFn: checkSecretsTh},

		{Name: doltLogDepthName, Chain: "tail", OnStoreNil: "warn", ThFn: checkDoltLogDepthTh},
		{Name: "db_size", Chain: "tail", OnStoreNil: "warn", ThFn: checkDBSizeTh},

		{Name: "dropped_dbs", Chain: "tail", OnStoreNil: "", ThFn: checkDroppedDBsTh},
		{Name: "stray_dbs", Chain: "tail", OnStoreNil: "", ThFn: checkStrayDBsTh},

		{Name: "engine-footprint", Chain: "tail", OnStoreNil: "", Fn: checkEngineFootprint},

		{Name: "engine", Chain: "tail", OnStoreNil: "", Fn: checkEngine},

		{Name: "versions", Chain: "tail", OnStoreNil: "", ThFn: checkVersionsTh},

		{Name: "hook-path", Chain: "tail", OnStoreNil: "",
			Fn: func(ctx context.Context, st wire.Store) DoctorCheck { return CheckHookPath() }},
		{Name: "dsn-class", Chain: "tail", OnStoreNil: "",
			Fn: func(ctx context.Context, st wire.Store) DoctorCheck { return CheckDSNClass() }},
		{Name: "collection-freshness", Chain: "tail", OnStoreNil: "warn", ThFn: CheckFreshnessTh},
		{Name: "plugin-version-gap", Chain: "tail", OnStoreNil: "", ThFn: CheckPluginGapTh},
		{Name: "service-remnant", Chain: "tail", OnStoreNil: "",
			Fn: func(ctx context.Context, st wire.Store) DoctorCheck { return CheckServiceRemnant() }},
	}
}

func doctorThresholds(ctx context.Context, st wire.Store) func() store.Thresholds {
	var (
		th     store.Thresholds
		loaded bool
	)
	return func() store.Thresholds {
		if !loaded {
			if st != nil {
				th = knowledge.WriteThresholds(ctx, st)
			} else {
				th = store.DefaultThresholds()
			}
			loaded = true
		}
		return th
	}
}

func runCheckSpec(ctx context.Context, st wire.Store, th func() store.Thresholds, s CheckSpec) DoctorCheck {
	if s.ThFn != nil {
		return s.ThFn(ctx, st, th())
	}
	return s.Fn(ctx, st)
}

func doctorSpec(name string) (CheckSpec, bool) {
	for _, s := range doctorChecks() {
		if s.Name == name {
			return s, true
		}
	}
	return CheckSpec{}, false
}

func doctorChainNames(chain string) []string {
	var names []string
	for _, s := range doctorChecks() {
		if s.Chain == chain {
			names = append(names, s.Name)
		}
	}
	return names
}

func DoctorChecks() []string {
	return doctorChainNames("core")
}

func doctorTailChecks() []string {
	return doctorChainNames("tail")
}

type DoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

type doctorOutput struct {
	Status string        `json:"status"`
	Checks []DoctorCheck `json:"checks"`
}

func RunDoctor(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {

	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--list-checks"},
			{Name: "--fix"},

			{Name: "--json"},
			{Name: "--check", Value: true},
		},
		Usage: i18n.T("usage.doctor"),
	})
	if perr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	if res.Given("--list-checks") {

		names := make([]string, 0, len(doctorChecks()))
		for _, s := range doctorChecks() {
			names = append(names, s.Name)
		}
		b, _ := json.Marshal(map[string][]string{"checks": names})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	subset, fix, jsonOut := res.Value("--check"), res.Given("--fix"), res.Given("--json")
	if res.Given("--check") && !res.HasValue("--check") {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", i18n.T("err.check_requires_name")+" — "+i18n.T("usage.doctor"))))
		return 3
	}
	if subset != "" {

		_, ok := doctorSpec(subset)
		if subset == "skill-file" || strings.HasPrefix(subset, "harness-") {
			ok = true
		}
		if !ok {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("doctor: unknown check %q — "+i18n.T("usage.doctor")+" (names via --list-checks)", subset)))
			return 3
		}
		checks := []DoctorCheck{runDoctorCheck(ctx, st, subset)}
		bumpDoctorWarnCounters(ctx, st, checks)
		return doctorReport(checks, fix, jsonOut, stdout)
	}

	th := doctorThresholds(ctx, st)
	checks := make([]DoctorCheck, 0, len(doctorChecks())+len(HarnessRegistry))
	for _, s := range doctorChecks() {
		if s.Chain == "core" {
			checks = append(checks, runCheckSpec(ctx, st, th, s))
		}
	}

	checks = append(checks, harnessChecks()...)

	if home := ClaudeHome(); home != "" {
		if c, ok := CheckSkillFileFn(SkillSrcDirFn(FlintBinPathFn()), filepath.Join(home, "skills", "flint")); ok {
			checks = append(checks, c)
		}
	}
	for _, s := range doctorChecks() {
		if s.Chain == "tail" {
			checks = append(checks, runCheckSpec(ctx, st, th, s))
		}
	}
	bumpDoctorWarnCounters(ctx, st, checks)
	return doctorReport(checks, fix, jsonOut, stdout)
}

func bumpDoctorWarnCounters(ctx context.Context, st wire.Store, checks []DoctorCheck) {
	if st == nil {
		return
	}
	for _, c := range checks {
		if c.Status == "warn" {
			knowledge.BumpMetaCounter(ctx, st, "fired_doctor_"+c.Name)
		}
	}
}

func doctorReport(checks []DoctorCheck, fix bool, jsonOut bool, stdout io.Writer) int {

	fixed := false
	if fix {
		for i, c := range checks {
			if c.Name != "hook-file" || c.Status != "fail" {
				continue
			}
			path := filepath.Join(ClaudeHome(), "settings.json")
			bin := FlintBinPathFn()
			applied, _, err := ApplyHookFix(path, bin)
			if err != nil {
				fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("doctor --fix: hook repair aborted, file unchanged: %w", err)))
				return 1
			}
			if applied {
				checks[i] = checkHookFile()
				fixed = true
			}
		}
	}
	overall := "ok"
	for _, c := range checks {
		switch c.Status {
		case "fail":
			overall = "fail"
		case "warn":
			if overall != "fail" {
				overall = "warn"
			}
		}
	}
	if fixed && !jsonOut {
		fmt.Fprintln(stdout, DoctorFixReport(checks))
		if overall == "fail" {
			return 1
		}
		return 0
	}
	b, err := json.Marshal(doctorOutput{Status: overall, Checks: checks})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	if overall == "fail" {
		return 1
	}
	return 0
}

func runDoctorCheck(ctx context.Context, st wire.Store, name string) DoctorCheck {
	if s, ok := doctorSpec(name); ok {
		return runCheckSpec(ctx, st, doctorThresholds(ctx, st), s)
	}
	switch name {
	case "skill-file":

		if home := ClaudeHome(); home != "" {
			if c, ok := CheckSkillFileFn(SkillSrcDirFn(FlintBinPathFn()), filepath.Join(home, "skills", "flint")); ok {
				return c
			}
		}
		return DoctorCheck{Name: name, Status: "ok", Detail: "skill source not found — check skipped"}
	}
	if strings.HasPrefix(name, "harness-") {

		for _, h := range HarnessRegistry {
			if "harness-"+h.Name == name {
				if c, ok := checkHarnessFile(h); ok {
					return c
				}
				return DoctorCheck{Name: name, Status: "ok", Detail: "harness not installed — check skipped"}
			}
		}
	}
	return DoctorCheck{Name: name, Status: "fail", Detail: "unknown check"}
}

func DoctorFixReport(checks []DoctorCheck) string {
	m := map[string]any{"status": "fixed"}
	for _, c := range checks {
		m[c.Name] = c.Status
	}
	b, err := json.Marshal(m)
	if err != nil {
		return `{"status":"fixed"}`
	}
	return string(b)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func gapSuffix(gap string) string {
	if gap == "" {
		return "— clears after invalidating the targets "
	}
	return gap + " "
}

package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"filippo.io/age"

	"github.com/synuns/flint/lib/backup"
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/i18n"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

type staleSessionStore interface {
	StaleSessionRuns(ctx context.Context, agent string) (int, error)
}

type usageStore interface {
	LogPlaybookUse(ctx context.Context, id int, ok bool) error
}

func logPlaybookUse(ctx context.Context, st wire.Store, id int, ok bool) {
	if id <= 0 {
		return
	}
	us, can := st.(usageStore)
	if !can {
		return
	}
	if err := us.LogPlaybookUse(ctx, id, ok); err != nil {
		fmt.Fprintf(os.Stderr, "[flint] playbook use log: %v\n", err)
	}
}

type DoneResult struct {
	Agent      string
	Unrecorded int

	Summary string
}

const sessionEndPrefix = "session end"

func (d *DoneResult) SessionRecord() map[string]any {
	return map[string]any{
		"kind":    "session",
		"agent":   d.Agent,
		"summary": fmt.Sprintf("%s (unrecorded %d)", sessionEndPrefix, d.Unrecorded),
		"tasks":   "[]",
	}
}

type sessionSummaryStore interface {
	UpdateSessionSummary(ctx context.Context, id int, summary, tasks string) (int64, error)
}

func writeSessionRecord(ctx context.Context, st wire.Store, rec map[string]any) int {
	agent, _ := rec["agent"].(string)

	warnSecrets("session agent", agent)
	agent = store.SanitizeText(agent)
	rec["agent"] = agent
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT id FROM sessions WHERE agent = $1
		 ORDER BY ts DESC, id DESC LIMIT 1`, []any{agent})
	if err == nil && len(rows) > 0 && rows[0][0] != nil {

		var id int
		if n, _ := fmt.Sscan(fmt.Sprint(rows[0][0]), &id); n == 1 {

			us, ok := st.(sessionSummaryStore)
			if !ok {
				fmt.Fprintln(os.Stderr, "[flint] session summary: unsupported store")
				return id
			}
			summary, _ := rec["summary"].(string)
			tasks, _ := rec["tasks"].(string)
			if _, err := us.UpdateSessionSummary(ctx, id, summary, tasks); err != nil {
				fmt.Fprintf(os.Stderr, "[flint] session summary: %v\n", err)
			}
			return id
		}
	}
	if _, err := st.InsertBatch(ctx, []map[string]any{rec}); err != nil {
		fmt.Fprintf(os.Stderr, "[flint] session summary: %v\n", err)
	}
	return 0
}

const hookDefaultAgent = "claude"

var hookSessionUUIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func agentForSession(ctx context.Context, st wire.Store, session string) string {
	if id, err := strconv.Atoi(session); err == nil {
		rows, err := wire.QueryOnce(ctx, st, `SELECT agent FROM sessions WHERE id = $1`, []any{id})
		if err == nil && len(rows) > 0 {
			if a, ok := rows[0][0].(string); ok && a != "" {
				return a
			}
		}
	}
	if hookSessionUUIDRe.MatchString(session) {
		return hookDefaultAgent
	}
	return session
}

func staleRuns(ctx context.Context, st wire.Store, agent string) (int, error) {
	if ss, ok := st.(staleSessionStore); ok {
		return ss.StaleSessionRuns(ctx, agent)
	}
	return 0, nil
}

func RunDone(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	session := ""
	sessionGiven := false
	playbook, playbookOK := 0, true
	for i := 0; i < len(args); i++ {
		if args[i] == "--session" {
			sessionGiven = true
			if i+1 < len(args) {
				session = args[i+1]
				i++
			}
		}
		if args[i] == "--playbook" && i+1 < len(args) {

			if n, err := strconv.Atoi(args[i+1]); err == nil {
				playbook = n
			}
			i++
		}
		if args[i] == "--playbook-ok" && i+1 < len(args) {
			if b, err := strconv.ParseBool(args[i+1]); err == nil {
				playbookOK = b
			}
			i++
		}
	}

	if err := validateSessionLabel(sessionGiven, session); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	if session == "" {

		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", i18n.T("usage.done"))))
		return 3
	}

	th := WriteThresholds(ctx, st)
	agent := agentForSession(ctx, st, session)
	runs, err := staleRuns(ctx, st, agent)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 0
	}

	res := &DoneResult{Agent: agent, Unrecorded: runs, Summary: doneStdinSummary(stdin)}

	if runs >= th.StaleSessions {
		fmt.Fprintf(stdout, `{"status":"stale","runs":%d,"warning":"%d consecutive sessions unrecorded"}`+"\n", runs, runs)

		BumpMetaCounter(ctx, st, "fired_done_stale")
	} else if w, starved := recallStarvedToday(ctx, st, th); starved {

		fmt.Fprintf(stdout, `{"status":"recall","writes":%d,"warning":"0 recalls vs %d writes today — run flint read before work next session"}`+"\n", w, w)

		BumpMetaCounter(ctx, st, "recall_starved_count")
		if merr := writeTypedMarker(markerKindRecall, session, nudgeReasonRecall); merr != nil {
			fmt.Fprintf(os.Stderr, "[flint] recall marker: %v\n", merr)
		}
	} else {
		fmt.Fprintln(stdout, `{"status":"ok"}`)
	}
	sessionRowID := writeSessionRecord(ctx, st, res.SessionRecord())

	maybeSessionKnowledge(ctx, st, res.Summary, agent, session, sessionRowID)
	logPlaybookUse(ctx, st, playbook, playbookOK)

	if _, cerr := st.Commit(ctx, fmt.Sprintf(
		"session-end: agent=%s runs=%d (session-end audit attribution) [session:0]",
		agentLabel(agent, 24), runs)); cerr != nil {
		fmt.Fprintf(os.Stderr, "[flint] session-end commit: %v\n", cerr)
	}

	commitTelemetry(ctx, st, "done")
	AutoBackupFn(ctx, st)
	return 0
}

func maybeSessionKnowledge(ctx context.Context, st wire.Store, summary, agent, session string, sessionRowID int) {

	summary = render.TruncateRunes(store.SanitizeText(strings.TrimSpace(summary)), 500)

	if summary == "" || len([]rune(summary)) < 12 {
		return
	}
	th := WriteThresholds(ctx, st)
	if th.SessionKnowledgeDays <= 0 {
		return
	}

	getKV, _ := metaKV(ctx, st)
	if v, err := getKV("session_knowledge_last"); err == nil && v != "" {

		if last, perr := time.Parse(time.RFC3339, v); perr == nil &&
			time.Since(last) < db.DaysSpan(th.SessionKnowledgeDays, time.Now()) {
			return
		}
	}

	topic := store.SanitizeText(render.TruncateRunes(
		fmt.Sprintf("session[%s] %s", agentLabel(agent, 24), summaryKeyword(summary)), 80))
	rec := map[string]any{
		"kind":       "knowledge",
		"topic":      topic,
		"summary":    summary,
		"tags":       []string{"process"},
		"domain":     "main",
		"confidence": 0.5,
		"status":     "pending",
		"origin":     "hook",
	}

	if sessionRowID <= 0 {
		sessionRowID = sessionRowIDForAgent(ctx, st, agent)
	}
	if sessionRowID > 0 {
		rec["session_id"] = sessionRowID
	}
	rec = store.Normalize("knowledge", rec)
	rec["content_hash"] = store.ContentHash(store.HashCanonical(rec))
	if exists, err := st.ExistsHash(ctx, rec["content_hash"].(string)); err == nil && exists {
		return
	}

	warnSecrets("session knowledge", summary)
	if _, err := st.InsertBatch(ctx, []map[string]any{rec}); err != nil {

		fmt.Fprintf(os.Stderr, "[flint] session knowledge: %v\n", err)
		return
	}

	if session != "" {
		if merr := writeTypedMarker(markerKindDistill, session, "session-knowledge"); merr != nil {
			fmt.Fprintf(os.Stderr, "[flint] distill marker: %v\n", merr)
		}
	}
	_, setKV := metaKV(ctx, st)
	if setKV != nil {
		_ = setKV("session_knowledge_last", time.Now().UTC().Format(time.RFC3339))
	}
}

func summaryKeyword(summary string) string {
	first := strings.SplitN(summary, "\n", 2)[0]

	cut := -1
	for _, sep := range []string{"。", "다.", ". ", "— "} {
		if i := strings.Index(first, sep); i > 4 && (cut < 0 || i < cut) {
			cut = i + len(sep)
		}
	}
	if cut > 0 {
		first = first[:cut]
	}
	return strings.TrimSpace(render.TruncateRunes(first, 20))
}

func sessionRowIDForAgent(ctx context.Context, st wire.Store, agent string) int {
	if agent == "" {
		return 0
	}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT id FROM sessions WHERE agent = $1 ORDER BY ts DESC, id DESC LIMIT 1`,
		[]any{agent})
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return 0
	}
	var id int
	if n, _ := fmt.Sscan(fmt.Sprint(rows[0][0]), &id); n == 1 {
		return id
	}
	return 0
}

func warnSecrets(prefix, s string) {
	for _, name := range store.ScanSecrets(s) {
		fmt.Fprintf(os.Stderr, "[flint] %s: possible secret: %s\n", prefix, name)
	}
}

func agentLabel(agent string, max int) string {
	if agent == "" {
		return "unknown"
	}
	return render.TruncateRunes(agent, max)
}

func recallStarvedToday(ctx context.Context, st wire.Store, th store.Thresholds) (writes int, starved bool) {
	if th.RecallWarnWrites <= 0 {
		return 0, false
	}
	day := time.Now().UTC().Format("2006-01-02")
	getKV, _ := metaKV(ctx, st)
	v, err := getKV("daily:write:" + day)
	if err != nil {
		return 0, false
	}
	if writes, err = strconv.Atoi(strings.TrimSpace(v)); err != nil {
		return 0, false
	}
	if writes < th.RecallWarnWrites {
		return writes, false
	}
	cutoff, cerr := time.Parse("2006-01-02", day)
	if cerr != nil {
		return 0, false
	}
	rows, qerr := wire.QueryOnce(ctx, st,
		`SELECT count(*) FROM read_events WHERE ts >= $1 AND ts < $2`,
		[]any{cutoff, cutoff.AddDate(0, 0, 1)})
	if qerr != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return 0, false
	}
	var reads int
	if n, _ := fmt.Sscan(fmt.Sprint(rows[0][0]), &reads); n != 1 {
		return 0, false
	}
	return writes, reads == 0
}

var AutoBackupFn = maybeAutoBackup

func maybeAutoBackup(ctx context.Context, st wire.Store) {
	if st == nil {
		return
	}
	dir := os.Getenv("FLINT_BACKUP_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		dir = filepath.Join(home, ".flint", "backup")
	}

	metaGet := func(key string) (string, error) {
		v, _, err := wire.GetMetaKVErr(ctx, st, key)
		return v, err
	}

	claimKV := func(oldVal, newVal string) (bool, error) {
		return wire.CompareSetMetaKV(ctx, st, "last_backup", oldVal, newVal)
	}
	lf := &lazyDumpFile{dir: dir}

	if v, ok := wire.GetMetaKV(ctx, st, "backup_encrypt"); ok && wire.BackupEncryptOn(v) {
		lf.encrypt = true
	}
	defer lf.close()
	auto := backup.Auto{DSN: wire.DSNFromEnv()}
	runErr := auto.RunIfNeeded(ctx, metaGet, claimKV, lf)

	if runErr == nil {
		if name := lf.closeAndPath(); name != "" {
			if _, rerr := rotateFn(dir, keepFn(metaGet)); rerr != nil {
				fmt.Fprintf(os.Stderr, "[flint] backup auto rotate: %v\n", rerr)
			}
		}
	}
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "[flint] backup auto: %v\n", runErr)
	}
}

func metaKV(ctx context.Context, st wire.Store) (func(string) (string, error), func(string, string) error) {
	getKV := func(key string) (string, error) {
		v, ok, err := wire.GetMetaKVErr(ctx, st, key)
		if err != nil || !ok {
			return "", err
		}
		return v, nil
	}
	setKV := func(key, value string) error {
		return wire.SetMetaKV(ctx, st, key, value)
	}
	return getKV, setKV
}

const (
	markerKindDistill = "distill"
	markerKindRecall  = "recall"
)

const nudgeReasonRecall = "recall"

var WriteTypedMarkerFn func(kind, sessionID, reason string) error

func writeTypedMarker(kind, sessionID, reason string) error {
	if WriteTypedMarkerFn == nil {
		return nil
	}
	return WriteTypedMarkerFn(kind, sessionID, reason)
}

var BackupKeyPathFn func() string

var (
	rotateFn    = backup.Rotate
	keepFn      = backup.BackupKeep
	ensureKeyFn = backup.EnsureIdentity
)

type lazyDumpFile struct {
	dir     string
	encrypt bool
	once    sync.Once
	f       *os.File
	h       hash.Hash
	hw      *hashFile
	aw      io.WriteCloser
	failed  bool
}

type hashFile struct {
	f *os.File
	h hash.Hash
}

func (w *hashFile) Write(p []byte) (int, error) {
	n, err := w.f.Write(p)
	if n > 0 {
		w.h.Write(p[:n])
	}
	return n, err
}

func (l *lazyDumpFile) Write(p []byte) (int, error) {
	l.once.Do(func() {
		if err := os.MkdirAll(l.dir, 0o700); err != nil {
			l.failed = true
			return
		}
		ext := ".sql"
		if l.encrypt {
			ext = ".sql.age"
		}

		var recipient *age.X25519Recipient
		if l.encrypt {
			if BackupKeyPathFn == nil {
				l.failed = true
				return
			}
			key := BackupKeyPathFn()
			if key == "" {
				l.failed = true
				return
			}
			id, err := ensureKeyFn(key)
			if err != nil {
				l.failed = true
				return
			}
			recipient = id.Recipient()
		}

		name := filepath.Join(l.dir,
			"flint-"+time.Now().UTC().Format("20060102-150405")+"-"+strconv.Itoa(os.Getpid())+ext)
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			l.failed = true
			return
		}
		l.f = f
		l.h = sha256.New()
		l.hw = &hashFile{f: f, h: l.h}
		if l.encrypt {
			aw, aerr := age.Encrypt(l.hw, recipient)
			if aerr != nil {
				l.failed = true
				return
			}
			l.aw = aw
		}
	})
	if l.failed {
		return 0, errors.New("backup auto: cannot create dump file")
	}

	if l.aw != nil {
		n, err := l.aw.Write(p)
		if err != nil {
			l.failed = true
		}
		return n, err
	}
	n, err := l.hw.Write(p)
	if err != nil {
		l.failed = true
		return n, err
	}
	return n, nil
}

func (l *lazyDumpFile) close() error {
	if l.f == nil {
		return nil
	}
	name := l.f.Name()
	if l.aw != nil {
		if aerr := l.aw.Close(); aerr != nil {
			l.failed = true
			_ = l.f.Close()
			l.f = nil
			_ = os.Remove(name)
			return errors.New("backup auto: age close failed — partial dump discarded")
		}
		l.aw = nil
	}
	closeErr := l.f.Close()
	l.f = nil

	if l.failed {
		_ = os.Remove(name)
		return errors.New("backup auto: partial dump discarded")
	}
	if closeErr != nil {
		return closeErr
	}
	sum := hex.EncodeToString(l.h.Sum(nil))
	if err := backup.WriteSidecar(name, sum); err != nil {
		fmt.Fprintf(os.Stderr, "[flint] backup sidecar: %v\n", err)
		return fmt.Errorf("sidecar: %w", err)
	}
	return nil
}

func (l *lazyDumpFile) closeAndPath() string {
	if l.f == nil {
		return ""
	}
	name := l.f.Name()
	if err := l.close(); err != nil {
		return ""
	}
	return name
}

func init() {
	wire.Register("done", RunDone)
}

func BumpMetaCounter(ctx context.Context, st wire.Store, key string) {
	if st == nil {
		return
	}
	_ = wire.BumpMetaKV(ctx, st, key)
}

func doneStdinSummary(stdin io.Reader) string {
	if stdin == nil {
		return ""
	}
	b, err := io.ReadAll(stdin)
	if err != nil {
		return ""
	}
	var h struct {
		CompactSummary string `json:"compact_summary"`
	}
	if json.Unmarshal(b, &h) != nil {
		return ""
	}
	return strings.TrimSpace(h.CompactSummary)
}

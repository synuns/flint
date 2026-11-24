package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

type SessionStartRecorder interface {
	RecordSessionStart(ctx context.Context, session string) error
}

func sanitizeHookSession(args []string) ([]string, string) {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--session" {
			val := ""
			if i+1 < len(args) {
				val = args[i+1]
				i++
			}
			if knowledge.ValidateSessionLabel(true, val) == nil {
				out = append(out, "--session", val)
			}
			continue
		}
		out = append(out, args[i])
	}
	return out, SessionLabel(out)
}

func sessionLabelMaxAge(ctx context.Context, st wire.Store) time.Duration {
	d := knowledge.WriteThresholds(ctx, st).EventsRetentionD
	if d <= 0 {
		d = 14
	}
	return db.DaysSpan(d, time.Now())
}

var runSessionHotFn = knowledge.RunHot

var injectActiveRecallFn = InjectActiveRecall

const recallActiveMarkerPrefix = "recall_active:"

type byteCountWriter struct {
	w io.Writer
	n int
}

func (b *byteCountWriter) Write(p []byte) (int, error) {
	n, err := b.w.Write(p)
	b.n += n
	return n, err
}

func injectActiveRecallOnce(ctx context.Context, st wire.Store, label string, stdout io.Writer, hotUsed int) {
	if st == nil || label == "" {
		return
	}
	key := recallActiveMarkerPrefix + label
	if _, ok, err := wire.GetMetaKVErr(ctx, st, key); err != nil || ok {
		return
	}
	defer func() { _ = recover() }()
	_ = injectActiveRecallFn(ctx, st, label, stdout, hotUsed)
	_ = wire.SetMetaKV(ctx, st, key, time.Now().UTC().Format(time.RFC3339))
}

func RunSessionStart(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	args, label := sanitizeHookSession(args)
	if rec, ok := st.(SessionStartRecorder); ok {
		_ = rec.RecordSessionStart(ctx, label)
	}

	if st != nil && CurrentVersionFn != nil && ValidVersionFn != nil {
		if v := CurrentVersionFn(); ValidVersionFn(v) {
			_ = wire.SetMetaKV(ctx, st, "self_version_last:session-start",
				v+"|"+time.Now().UTC().Format(time.RFC3339))
		}
	}

	_ = WriteSessionLabel(label)
	if label != "" {
		wire.PruneSessionLabels(sessionLabelMaxAge(ctx, st))
	}
	hotOut := &byteCountWriter{w: stdout}
	code := runSessionHotFn(ctx, st, args, stdin, hotOut)
	injectActiveRecallOnce(ctx, st, label, stdout, hotOut.n)
	if ncode := RunNudge(ctx, st, nil, stdin, stdout); ncode != 0 {
		return ncode
	}

	if MaybeUpdateNoticeFn != nil {
		if line := MaybeUpdateNoticeFn(); line != "" {
			fmt.Fprintln(stdout, line)

			knowledge.BumpMetaCounter(ctx, st, "fired_nudge_update")
		}
	}

	if line := maybeMaintenanceNotice(ctx, st); line != "" {
		fmt.Fprintln(stdout, line)
	}
	return code
}

var MaybeUpdateNoticeFn func() string

var UpdateNoticeTTL = 24 * time.Hour

type maintenanceNoticeCache struct {
	CheckedAt string `json:"checked_at"`
}

func maintenanceCacheFile() string {
	return DataFile("maintenance-check.json")
}

func maybeMaintenanceNotice(ctx context.Context, st wire.Store) string {
	path := maintenanceCacheFile()
	if path == "" {
		return ""
	}
	if b, err := os.ReadFile(path); err == nil {
		var c maintenanceNoticeCache
		if json.Unmarshal(b, &c) == nil {
			if checked, err := time.Parse(time.RFC3339, c.CheckedAt); err == nil &&
				time.Since(checked) < UpdateNoticeTTL {
				return ""
			}
		}
	}
	th := knowledge.WriteThresholds(ctx, st)

	n, err := knowledge.CountPending(ctx, st)
	if err != nil {
		return ""
	}
	_ = os.WriteFile(path, []byte(`{"checked_at":"`+time.Now().UTC().Format(time.RFC3339)+`"}`), 0o600)
	if n > th.PendingMax {
		return "[flint] pending " + strconv.Itoa(n) + " items (limit " + strconv.Itoa(th.PendingMax) + ") — check flint doctor and maintain"
	}
	return ""
}

func SessionLabel(args []string) string {
	s := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--session" && i+1 < len(args) {
			s = args[i+1]
			i++
		}
	}
	return s
}

func RunSessionEnd(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {

	args, label := sanitizeHookSession(args)

	ClearSessionLabel(label)
	code := knowledge.RunDone(ctx, st, args, stdin, stdout)
	if ncode := RunNudge(ctx, st, nil, stdin, stdout); ncode != 0 {
		return ncode
	}
	return code
}

package wire

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

var ErrInvalidAsOf = errors.New("invalid as-of spec")

const AsofMinPrefix = 3

const maxRelativeAsOfHours = int(math.MaxInt64 / int64(time.Hour))

const asofZeroRetryDelay = 100 * time.Millisecond

func ParseRelative(spec string) (time.Time, error) {
	fail := fmt.Errorf("invalid relative as-of spec %q (only integer Nh·Nd form allowed): %w", spec, ErrInvalidAsOf)
	if len(spec) < 2 {
		return time.Time{}, fail
	}
	unit := spec[len(spec)-1]
	if unit != 'h' && unit != 'd' {
		return time.Time{}, fail
	}
	v, err := strconv.Atoi(spec[:len(spec)-1])
	if err != nil || v < 0 {
		return time.Time{}, fail
	}
	if unit == 'd' {

		if v > maxRelativeAsOfHours/24 {
			return time.Time{}, asofCeilingErr(spec)
		}
		v *= 24
	}
	if v > maxRelativeAsOfHours {
		return time.Time{}, asofCeilingErr(spec)
	}
	return time.Now().Add(-time.Duration(v) * time.Hour), nil
}

func asofCeilingErr(spec string) error {
	return fmt.Errorf(
		"relative as-of spec %q exceeds the %dh Duration ceiling (largest day form: 106751d): %w",
		spec, maxRelativeAsOfHours, ErrInvalidAsOf)
}

var doltAutoCommitMsgs = map[string]bool{
	"Initialize data repository": true,
	"CREATE DATABASE":            true,
}

func adoptOrPreFlint(ctx context.Context, st Store, spec string, e LogEntry) (string, error) {
	if !doltAutoCommitMsgs[e.Message] {
		return e.Hash, nil
	}
	_, err := st.QueryAsOf(ctx, AsOfQuery{
		Hash:    e.Hash,
		Table:   "knowledge",
		Columns: []string{"1"},
		Limit:   1,
	})
	if err == nil {

		return e.Hash, nil
	}
	if isTableAbsentAtCommit(err) {
		return "", asofPreFlintErr(spec)
	}
	return "", err
}

func isTableAbsentAtCommit(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "table not found") ||
		strings.Contains(msg, "references invalid table(s)")
}

func asofPreFlintErr(spec string) error {
	return fmt.Errorf(
		"as-of spec %q resolves to a pre-flint commit (no flint schema exists there) — use an as-of at or after the first flint write: %w",
		spec, ErrInvalidAsOf)
}

func ParseAsOfSpec(ctx context.Context, st Store, spec string) (string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", fmt.Errorf("empty as-of spec: %w", ErrInvalidAsOf)
	}

	t, relErr := ParseRelative(spec)
	if relErr == nil {
		return commitAtOrBefore(ctx, st, spec, t)
	}

	if relativeShaped(spec) {
		return "", relErr
	}

	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04"} {
		if t, err := time.Parse(layout, spec); err == nil {
			return commitAtOrBefore(ctx, st, spec, t)
		}
	}

	if len(spec) >= AsofMinPrefix && isLowerAlnumPrefix(spec) {
		entries, err := st.LogEntries(ctx, LogQuery{Prefix: spec})
		if err != nil {
			return "", err
		}
		for attempt := 0; len(entries) == 0 && attempt < 2; attempt++ {
			time.Sleep(asofZeroRetryDelay)
			entries, err = st.LogEntries(ctx, LogQuery{Prefix: spec})
			if err != nil {
				return "", err
			}
		}
		switch len(entries) {
		case 1:
			return adoptOrPreFlint(ctx, st, spec, entries[0])
		case 0:
			return "", fmt.Errorf("no commit matches prefix %q: %w", spec, ErrInvalidAsOf)
		default:
			return "", fmt.Errorf("prefix %q matches %d commits (ambiguous — use more characters): %w", spec, len(entries), ErrInvalidAsOf)
		}
	}
	return "", fmt.Errorf("invalid as-of spec %q (ISO datetime, Nh/Nd, or hash prefix 3+ chars): %w", spec, ErrInvalidAsOf)
}

func relativeShaped(spec string) bool {
	if len(spec) < 2 {
		return false
	}
	if u := spec[len(spec)-1]; u != 'h' && u != 'd' {
		return false
	}
	for i := 0; i < len(spec)-1; i++ {
		if spec[i] < '0' || spec[i] > '9' {
			return false
		}
	}
	return true
}

func commitAtOrBefore(ctx context.Context, st Store, spec string, t time.Time) (string, error) {
	entries, err := st.LogEntries(ctx, LogQuery{BeforeOrAt: t})
	if err != nil {
		return "", err
	}
	if len(entries) == 1 {

		return adoptOrPreFlint(ctx, st, spec, entries[0])
	}

	first, err := st.LogEntries(ctx, LogQuery{Earliest: true})
	if err != nil {
		return "", err
	}
	if len(first) == 1 {
		return "", fmt.Errorf("no commits exist yet at %s (first commit: %v): %w",
			spec, first[0].Date, ErrInvalidAsOf)
	}
	return "", fmt.Errorf("as-of cannot be used on a repository with no commits: %w", ErrInvalidAsOf)
}

func isLowerAlnumPrefix(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z')) {
			return false
		}
	}
	return true
}

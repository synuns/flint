// asof — point-in-time spec normalization. Resolves the 3 spec kinds (ISO time,
// relative Nh/Nd, hash prefix) to a single commit hash via Store.LogEntries.
// Time-travel queries always normalize to a commit hash (§3 — independent of AS OF
// SYSTEM TIME). An invalid spec is ErrInvalidAsOf (CLI exit 3).
// A shared Store utility — consumed by the knowledge/ops seams. cli re-exports it via an alias (cli/asof.go).
package wire

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidAsOf — an invalid as-of spec (format error, prefix ambiguity/not found,
// or before the first commit). Mapped to exit 3 at the command layer.
var ErrInvalidAsOf = errors.New("invalid as-of spec")

// AsofMinPrefix — the minimum hash-prefix length (3 chars). Below is a format error.
const AsofMinPrefix = 3

// ParseRelative — expands the relative specs "Nh" and "Nd" to a past time from now
// (Go-computed). d expands as h*24; a non-integer (2.5h, "2 days ago") is ErrInvalidAsOf.
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
		v *= 24
	}
	return time.Now().Add(-time.Duration(v) * time.Hour), nil
}

// ParseAsOfSpec — normalizes a spec to a commit hash. Verdict order: (1) relative
// (\\d+[hd] takes precedence as the documented relative form) (2) ISO (RFC3339,
// 2006-01-02T15:04) (3) hash prefix (lowercase alphanumeric, 3+ chars — Doltgres
// commit hashes are base32-like [0-9a-z] in full, observed). ISO and relative
// adopt the latest commit at or before the time; a prefix resolves only on exactly 1 match (0 or 2+ = ErrInvalidAsOf — uniqueness via a wire len check).
func ParseAsOfSpec(ctx context.Context, st Store, spec string) (string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", fmt.Errorf("empty as-of spec: %w", ErrInvalidAsOf)
	}
	// (1) relative spec — if not the form, move on to the next candidate.
	if t, err := ParseRelative(spec); err == nil {
		return commitAtOrBefore(ctx, st, spec, t)
	}
	// (2) ISO time.
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04"} {
		if t, err := time.Parse(layout, spec); err == nil {
			return commitAtOrBefore(ctx, st, spec, t)
		}
	}
	// (3) hash prefix — after the lowercase-alphanumeric check, fetches all matches
	// via LogQuery{Prefix} (order-agnostic). Uniqueness (0 or 2+ = ErrInvalidAsOf) via a len check.
	if len(spec) >= AsofMinPrefix && isLowerAlnumPrefix(spec) {
		entries, err := st.LogEntries(ctx, LogQuery{Prefix: spec})
		if err != nil {
			return "", err
		}
		switch len(entries) {
		case 1:
			return entries[0].Hash, nil
		case 0:
			return "", fmt.Errorf("no commit matches prefix %q: %w", spec, ErrInvalidAsOf)
		default:
			return "", fmt.Errorf("prefix %q matches %d commits (ambiguous — use more characters): %w", spec, len(entries), ErrInvalidAsOf)
		}
	}
	return "", fmt.Errorf("invalid as-of spec %q (ISO datetime, Nh/Nd, or hash prefix 3+ chars): %w", spec, ErrInvalidAsOf)
}

// commitAtOrBefore — adopts the latest commit at or before the time
// (LogQuery{BeforeOrAt} = the 1 newest row with date <= t). The date comparison passes the Go-computed time.Time as-is.
func commitAtOrBefore(ctx context.Context, st Store, spec string, t time.Time) (string, error) {
	entries, err := st.LogEntries(ctx, LogQuery{BeforeOrAt: t})
	if err != nil {
		return "", err
	}
	if len(entries) == 1 {
		return entries[0].Hash, nil
	}
	// 0 rows — a time before the first commit. The message includes the first commit time (following C11 Snowflake).
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

// isLowerAlnumPrefix — whether it is a lowercase-alphanumeric [0-9a-z] prefix
// (the empty string is false). Doltgres commit hashes are base32-like (the full
// [0-9a-z] range), so a hex-only check rejected every real-DB hash (a defect
// closed by observation). Uniqueness is handled by the len check on the LogEntries result.
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

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
	"math"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidAsOf — an invalid as-of spec (format error, prefix ambiguity/not found,
// or before the first commit). Mapped to exit 3 at the command layer.
var ErrInvalidAsOf = errors.New("invalid as-of spec")

// AsofMinPrefix — the minimum hash-prefix length (3 chars). Below is a format error.
const AsofMinPrefix = 3

// maxRelativeAsOfHours — the largest hour count whose time.Duration(v)*time.Hour
// stays inside int64 (the floor of the Duration ceiling, ~2562047h). A relative
// spec past it (e.g. 4000000h, or 106752d whose *24 exceeds the ceiling) wraps
// the Duration negative → a FUTURE as-of time, which commitAtOrBefore would
// silently resolve to the newest commit (exit 0 with a meaningless verdict).
// Past-ceiling values are rejected up front as an invalid spec (exit 3, the
// asof error convention); 106751d is the largest day count that still fits.
const maxRelativeAsOfHours = int(math.MaxInt64 / int64(time.Hour))

// ParseRelative — expands the relative specs "Nh" and "Nd" to a past time from now
// (Go-computed). d expands as h*24; a non-integer (2.5h, "2 days ago") is ErrInvalidAsOf,
// and so is an hour count past the Duration ceiling (v1.19.3 R77-1/R78-1) or a day count
// whose *24 would wrap int64 (v1.19.4 R79-1 — the guard sits BEFORE the multiply).
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
		// Pre-multiply guard (v1.19.4 R79-1): v*24 can wrap int64, and the
		// wrapped value slips past every later check (negative → not > ceiling)
		// — MaxInt64 days wraps to −24h (a FUTURE time → newest commit, exit 0)
		// and (2^64+8)/24 days wraps to +8h. maxRelativeAsOfHours/24 = 106751:
		// the same largest-fitting day form the ceiling error names.
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

// asofCeilingErr — the past-ceiling rejection shared by the hour and day paths.
func asofCeilingErr(spec string) error {
	return fmt.Errorf(
		"relative as-of spec %q exceeds the %dh Duration ceiling (largest day form: 106751d): %w",
		spec, maxRelativeAsOfHours, ErrInvalidAsOf)
}

// doltAutoCommitMsgs — the Doltgres automatic initial commit messages on a
// fresh database (observed: "Initialize data repository" by Dolt System
// Account, "CREATE DATABASE" at creation). No flint schema or views exist at
// those commits, so an as-of resolving to one fails the as-of view query with
// a raw SQL error (exit 1). v1.19.5 R81 — both normalization paths reject a
// resolved auto-commit up front as a usage error (exit 3) with a diagnostic;
// the check reads the Message of rows LogEntries already returned (no extra
// round-trip — the Prefix/BeforeOrAt queries widened to carry message).
// Exact-match only: a flint audit/telemetry commit message never collides
// ("knowledge: ...", "telemetry: ..."), and an unrecognized manual commit is
// left to the normal path (no new false rejections).
var doltAutoCommitMsgs = map[string]bool{
	"Initialize data repository": true,
	"CREATE DATABASE":            true,
}

// asofPreFlintErr — the pre-flint rejection shared by the time and prefix paths.
func asofPreFlintErr(spec string) error {
	return fmt.Errorf(
		"as-of spec %q resolves to a pre-flint commit (no flint schema exists there) — use an as-of at or after the first flint write: %w",
		spec, ErrInvalidAsOf)
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
	t, relErr := ParseRelative(spec)
	if relErr == nil {
		return commitAtOrBefore(ctx, st, spec, t)
	}
	// A spec SHAPED like the relative form that still failed (past the ceiling,
	// or a digit count past Atoi's own range) is a genuine relative error, not a
	// prefix candidate: returning it here keeps the ceiling wording reachable —
	// the prefix fallback would otherwise misreport it as "no commit matches
	// prefix" (v1.19.4 R79-2/R80-2). Unshaped failures (2.5h, "2 days ago")
	// keep falling through.
	if relativeShaped(spec) {
		return "", relErr
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
			if doltAutoCommitMsgs[entries[0].Message] {
				return "", asofPreFlintErr(spec)
			}
			return entries[0].Hash, nil
		case 0:
			return "", fmt.Errorf("no commit matches prefix %q: %w", spec, ErrInvalidAsOf)
		default:
			return "", fmt.Errorf("prefix %q matches %d commits (ambiguous — use more characters): %w", spec, len(entries), ErrInvalidAsOf)
		}
	}
	return "", fmt.Errorf("invalid as-of spec %q (ISO datetime, Nh/Nd, or hash prefix 3+ chars): %w", spec, ErrInvalidAsOf)
}

// relativeShaped — whether the spec is exactly the documented relative form
// [0-9]+[hd] (the shape alone — no range judgment; ParseRelative owns that).
// Used to give shaped specs the relative verdict even on failure instead of a
// misleading prefix lookup.
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

// commitAtOrBefore — adopts the latest commit at or before the time
// (LogQuery{BeforeOrAt} = the 1 newest row with date <= t). The date comparison passes the Go-computed time.Time as-is.
func commitAtOrBefore(ctx context.Context, st Store, spec string, t time.Time) (string, error) {
	entries, err := st.LogEntries(ctx, LogQuery{BeforeOrAt: t})
	if err != nil {
		return "", err
	}
	if len(entries) == 1 {
		// v1.19.5 R81 — the newest row at or before t is a Doltgres
		// auto-initial commit → no flint schema exists at that point.
		if doltAutoCommitMsgs[entries[0].Message] {
			return "", asofPreFlintErr(spec)
		}
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

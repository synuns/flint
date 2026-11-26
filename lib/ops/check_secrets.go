package ops

import (
	"context"
	"fmt"
	"strings"

	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

func checkSecrets(ctx context.Context, st wire.Store) DoctorCheck {
	return checkSecretsTh(ctx, st, doctorThresholds(ctx, st)())
}

func checkSecretsTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "secrets"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot query for the secrets scan"
		return c
	}

	rows, err := wire.QueryOnce(ctx, st, `
		SELECT id, topic, summary, tags, entity, domain, origin
		FROM knowledge WHERE invalid_at IS NULL
		LIMIT $1`, []any{th.SecretsScanRows})
	if err != nil {
		c.Status = "warn"
		c.Detail = "knowledge query failed — check the schema"
		return c
	}
	if len(rows) >= th.SecretsScanRows {

		c.Status = "warn"
		c.Detail = fmt.Sprintf("row limit %d reached — scan not run (large DB)", th.SecretsScanRows)
		c.Fix = "fix: fall back to write-time warnings — adjust the limit via the secrets_scan_row_limit key"
		return c
	}

	found := 0
	seen := map[string]bool{}
	for _, r := range rows {
		if len(r) < 7 {
			continue
		}
		text := strings.Join([]string{fmt.Sprint(r[1]), fmt.Sprint(r[2]), fmt.Sprint(r[3]), fmt.Sprint(r[4]), fmt.Sprint(r[5]), fmt.Sprint(r[6])}, " ")
		for _, name := range store.ScanSecrets(text) {
			id := fmt.Sprint(r[0])
			key := name + "#" + id
			if seen[key] {
				continue
			}
			seen[key] = true
			found++
		}
	}

	srows, serr := wire.QueryOnce(ctx, st, `
		SELECT id, agent, summary FROM sessions
		LIMIT $1`, []any{th.SecretsScanRows})
	gap := ""
	switch {
	case serr != nil:
		gap = "· session query failed (sessions unscanned)"
	case len(srows) >= th.SecretsScanRows:
		gap = "· session row limit reached (partial scan)"
	}
	sessionFound := false
	if serr == nil {
		for _, r := range srows {
			if len(r) < 3 {
				continue
			}
			text := fmt.Sprint(r[1]) + " " + fmt.Sprint(r[2])
			for _, name := range store.ScanSecrets(text) {
				key := name + "#session:" + fmt.Sprint(r[0])
				if !seen[key] {
					seen[key] = true
					found++
					sessionFound = true
				}
			}
		}
	}

	prows, perr := wire.QueryOnce(ctx, st, `
		SELECT id, name FROM projects
		LIMIT $1`, []any{th.SecretsScanRows})
	switch {
	case perr != nil:
		gap += "· project query failed (projects unscanned)"
	case len(prows) >= th.SecretsScanRows:
		gap += "· project row limit reached (partial scan)"
	}
	projectFound := false
	if perr == nil {
		for _, r := range prows {
			if len(r) < 2 {
				continue
			}
			for _, name := range store.ScanSecrets(fmt.Sprint(r[1])) {
				key := name + "#project:" + fmt.Sprint(r[0])
				if !seen[key] {
					seen[key] = true
					found++
					projectFound = true
				}
			}
		}
	}
	wrows, werr := wire.QueryOnce(ctx, st, `
		SELECT id, task, COALESCE(detail, ''), COALESCE(next_action, '')
		FROM work_state WHERE status = 'open'
		LIMIT $1`, []any{th.SecretsScanRows})
	switch {
	case werr != nil:
		gap += "· work_state query failed (work_state unscanned)"
	case len(wrows) >= th.SecretsScanRows:
		gap += "· work_state row limit reached (partial scan)"
	}
	workStateFound := false
	if werr == nil {
		for _, r := range wrows {
			if len(r) < 4 {
				continue
			}
			text := strings.Join([]string{fmt.Sprint(r[1]), fmt.Sprint(r[2]), fmt.Sprint(r[3])}, " ")
			for _, name := range store.ScanSecrets(text) {
				key := name + "#work_state:" + fmt.Sprint(r[0])
				if !seen[key] {
					seen[key] = true
					found++
					workStateFound = true
				}
			}
		}
	}
	if found == 0 && gap != "" {
		c.Status = "warn"
		c.Detail = "incomplete scan " + gap
		c.Fix = "fix: rerun flint doctor — if it persists, check the schema and secrets_scan_row_limit"
		return c
	}
	if found == 0 {
		c.Status = "ok"
		c.Detail = "no secrets found in knowledge, sessions, projects, or work_state"
		return c
	}
	c.Status = "warn"
	disp := make([]string, 0, 5)
	for _, k := range sortedKeys(seen) {
		if len(disp) == 5 {
			break
		}
		disp = append(disp, k)
	}

	c.Detail = fmt.Sprintf("%d secret candidates (%s) %s· allowlist %d entries",
		found, strings.Join(disp, ", "), gapSuffix(gap), store.SecretsAllowlistCount())
	if sessionFound || projectFound || workStateFound {
		c.Fix = "fix: for session/project/work_state findings (session:N, project:N, work_state:N), re-finalize the agent (done) or clean the sessions/projects/work_state rows in the operating DB — for knowledge findings, maintain --invalidate --id N --reason secret"
	} else {
		c.Fix = "fix: flint maintain --invalidate --id N --reason secret (rescan skips invalidated rows)"
	}
	return c
}

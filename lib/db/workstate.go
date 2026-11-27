package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"golang.org/x/text/unicode/norm"

	"github.com/synuns/flint/lib/store"
)

func NormalizeWorkTask(s string) string {
	s = strings.ToLower(strings.TrimSpace(norm.NFKC.String(s)))
	var b strings.Builder
	space := true
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteRune(' ')
			space = true
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func WorkStateHash(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func workStateRecFields(rec map[string]any) (*int, string, string, string) {
	var projectID *int
	setProject := func(v int64) {
		if v > 0 {
			p := int(v)
			projectID = &p
		}
	}
	switch v := rec["project_id"].(type) {
	case int:
		setProject(int64(v))
	case int64:
		setProject(v)
	}
	task, _ := rec["task"].(string)
	detail, _ := rec["detail"].(string)
	nextAction, _ := rec["next_action"].(string)
	return projectID, task, detail, nextAction
}

func SanitizeWorkStatePayload(rec map[string]any) []string {
	var warns []string
	for _, f := range []string{"detail", "next_action"} {
		s, ok := rec[f].(string)
		if !ok {
			continue
		}
		clean := store.SanitizeText(s)
		if clean != s {
			rec[f] = clean
		}
		for _, name := range store.ScanSecrets(clean) {
			warns = append(warns, "possible secret: "+name)
		}
	}
	return warns
}

func WarnWorkStateSecrets(warns []string) {
	for _, w := range warns {
		fmt.Fprintf(os.Stderr, "[flint] work_state open: %s\n", w)
	}
}

func OpenWorkStateQuery(projectID *int, hash string) (string, []any) {
	const base = `SELECT id, COALESCE(detail, ''), COALESCE(next_action, '') FROM work_state WHERE `
	if projectID == nil {
		return base + `project_id IS NULL AND state_hash = $1 AND status = 'open'`, []any{hash}
	}
	return base + `project_id = $1 AND state_hash = $2 AND status = 'open'`, []any{*projectID, hash}
}

func (r *Repo) OpenWorkState(ctx context.Context, rec map[string]any) (int, string, error) {
	WarnWorkStateSecrets(SanitizeWorkStatePayload(rec))
	projectID, task, detail, nextAction := workStateRecFields(rec)
	normalized := NormalizeWorkTask(task)
	if normalized == "" {
		return 0, "", fmt.Errorf("open work state: empty task")
	}
	hash := WorkStateHash(normalized)
	var id int
	var curDetail, curNext string
	sql, args := OpenWorkStateQuery(projectID, hash)
	err := r.pool.QueryRow(ctx, sql, args...).Scan(&id, &curDetail, &curNext)
	if errors.Is(err, pgx.ErrNoRows) {
		var newID int
		if err := r.pool.QueryRow(ctx,
			`INSERT INTO work_state (project_id, task, state_hash, detail, next_action, status)
			 VALUES ($1, $2, $3, $4, $5, 'open') RETURNING id`,
			projectID, normalized, hash, detail, nextAction).Scan(&newID); err != nil {
			return 0, "", fmt.Errorf("open work state: %w", err)
		}
		return newID, "open", nil
	}
	if err != nil {
		return 0, "", fmt.Errorf("open work state: %w", err)
	}
	if curDetail != detail || curNext != nextAction {
		if _, err := r.pool.Exec(ctx,
			`UPDATE work_state SET detail = $2, next_action = $3, updated_at = now() WHERE id = $1`,
			id, detail, nextAction); err != nil {
			return 0, "", fmt.Errorf("open work state: %w", err)
		}
		return id, "updated", nil
	}
	return id, "noop", nil
}

func (r *Repo) ResolveWorkState(ctx context.Context, id int, byKnowledgeID int) error {
	var by any
	if byKnowledgeID > 0 {
		by = byKnowledgeID
	}
	ct, err := r.pool.Exec(ctx,
		`UPDATE work_state SET status = 'resolved', resolved_by = $2, resolved_at = now() WHERE id = $1`,
		id, by)
	if err != nil {
		return fmt.Errorf("resolve work state: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("resolve work state: id %d not found", id)
	}
	return nil
}

func (r *Repo) BatchResolveWorkState(ctx context.Context, ids []int) (int, error) {
	ct, err := r.pool.Exec(ctx,
		`UPDATE work_state SET status = 'resolved', resolved_by = NULL, resolved_at = now()
		 WHERE id = ANY($1)`, ids)
	if err != nil {
		return 0, fmt.Errorf("batch resolve work state: %w", err)
	}
	return int(ct.RowsAffected()), nil
}

func (r *Repo) PurgeResolvedWorkState(ctx context.Context, cutoff time.Time) (int, error) {
	ct, err := r.pool.Exec(ctx,
		`DELETE FROM work_state
		 WHERE status = 'resolved' AND COALESCE(resolved_at, updated_at) < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("purge resolved work state: %w", err)
	}
	return int(ct.RowsAffected()), nil
}

func (r *Repo) ListOpenWorkState(ctx context.Context, projectID int, limit int) []map[string]any {
	sql := `SELECT id, project_id, task, state_hash, COALESCE(detail, ''), COALESCE(next_action, ''), status, resolved_by, updated_at
		FROM work_state
		WHERE status = 'open' AND resolved_by IS NULL`
	args := []any{}
	if projectID > 0 {
		args = append(args, projectID)
		sql += fmt.Sprintf(` AND project_id = $%d`, len(args))
	}
	sql += ` ORDER BY updated_at DESC`
	if limit > 0 {
		args = append(args, limit)
		sql += fmt.Sprintf(` LIMIT $%d`, len(args))
	}
	return r.workStateRows(ctx, sql, args...)
}

func (r *Repo) StaleOpenWorkStates(ctx context.Context, days int) []map[string]any {
	cutoff := time.Now().AddDate(0, 0, -days)
	return r.workStateRows(ctx,
		`SELECT id, project_id, task, state_hash, COALESCE(detail, ''), COALESCE(next_action, ''), status, resolved_by, updated_at
		FROM work_state
		WHERE status = 'open' AND updated_at < $1
		ORDER BY updated_at ASC`,
		cutoff)
}

func (r *Repo) workStateRows(ctx context.Context, sql string, args ...any) []map[string]any {
	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int
		var projectID *int
		var task, stateHash, detail, nextAction, status string
		var resolvedBy *int
		var updatedAt time.Time
		if err := rows.Scan(&id, &projectID, &task, &stateHash, &detail, &nextAction, &status, &resolvedBy, &updatedAt); err != nil {
			return out
		}
		out = append(out, map[string]any{
			"id":          id,
			"project_id":  projectID,
			"task":        task,
			"state_hash":  stateHash,
			"detail":      detail,
			"next_action": nextAction,
			"status":      status,
			"resolved_by": resolvedBy,
			"updated_at":  updatedAt,
		})
	}
	return out
}

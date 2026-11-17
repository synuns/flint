// sync.go — v1.7.0 T4: Doltgres 원격 등록(dolt_remote SQL 함수 래핑).
// v1.7.0 T5: push/pull(dolt_push·dolt_fetch+dolt_merge 래핑).
// 실측(dolthub/doltgresql, 2026-11-17):
//   - SELECT dolt_remote('add', name, url) → int 0. 중복명도 오류 없이
//     url을 조용히 덮어쓴다 — 중복 차단은 dolt_remotes 사전 조회 계약.
//   - 목록은 dolt_remote() 인자 없음 호출이 오류("use 'dolt_remotes'
//     system table") — SELECT name, url FROM dolt_remotes로 읽는다.
//   - SELECT dolt_remote('remove', name) → int 0. 미존재명은
//     "unknown remote" 오류 — 사전 조회로 ErrRemoteNotFound로 정규화.
// 쿼리 함수는 임의 Querier(wire.Store·그 fake가 구조적으로 만족 —
// BumpDailyFlatKey 관례)로 실행돼 CLI 배선이 쓰기 SQL을 db 소유 원칙
// 그대로 재사용한다. 원격 url은 개인 데이터 방어 대상 — 원문은 어떤
// 오류 메시지에도 실리지 않고, 출력은 MaskRemoteURL 경유가 계약.
package db

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// 원격 등록 쿼리 — fake 기반 CLI 테스트가 이 상수들로 SQL을 stub한다.
const (
	// SQLRemoteExists — 중복/미존재 판정 사전 조회.
	SQLRemoteExists = `SELECT 1 FROM dolt_remotes WHERE name = $1`
	// SQLRemoteAdd — dolt_remote 함수 래핑(신규 등록).
	SQLRemoteAdd = `SELECT dolt_remote('add', $1, $2)`
	// SQLRemoteList — dolt_remotes 시스템 테이블 조회(이름순 안정 정렬).
	SQLRemoteList = `SELECT name, url FROM dolt_remotes ORDER BY name`
	// SQLRemoteRemove — dolt_remote 함수 래핑(삭제).
	SQLRemoteRemove = `SELECT dolt_remote('remove', $1)`
	// SQLSyncRemoteURL — push/pull 대상 원격의 url 회수(사전 조회 겸
	// 마스킹 치환 원문 확보).
	SQLSyncRemoteURL = `SELECT name, url FROM dolt_remotes WHERE name = $1`
	// SQLSyncPush — dolt_push 함수 래핑(브랜치 'main' 고정). 반환 메시지에
	// url 원문이 포함되므로(실측) 결과 행은 읽지 않고 오류 여부만 본다.
	SQLSyncPush = `SELECT dolt_push($1, 'main')`
	// SQLSyncFetch — dolt_fetch 함수 래핑(원격 브랜치 'main'만).
	SQLSyncFetch = `SELECT dolt_fetch($1, $2)`
	// SQLSyncMerge — dolt_merge 함수 래핑. 인자는 <remote>/main(실측:
	// clone이 만든 추적 브랜치 참조 — 자유 브랜치명 아님).
	SQLSyncMerge = `SELECT dolt_merge($1)`
	// SQLSyncHeadHash — main 브랜치 head 해시(push 결과·pull 전후 비교).
	SQLSyncHeadHash = `SELECT hash FROM dolt_branches WHERE name = 'main'`
)

// 원격 오류 — CLI exit 코드 계약의 판별 기준(errors.Is).
var (
	// ErrRemoteExists — add 대상명이 이미 등록됨(dolt_remote 자체는
	// 조용히 덮어쓰므로 사전 조회로만 잡는다).
	ErrRemoteExists = errors.New("remote name already exists")
	// ErrRemoteNotFound — remove 대상명이 등록되지 않음.
	ErrRemoteNotFound = errors.New("remote not found")
	// ErrMergeConflict — pull merge 충돌(자동 해결 금지 계약 — 안내 문구
	// 동반, 원인은 래핑 보존).
	ErrMergeConflict = errors.New("sync pull: merge conflict")
)

// MergeConflictGuide — 충돌 시 수동 해결 안내(자동 해결 금지 — 파괴 방지).
const MergeConflictGuide = "automatic resolution is disabled; resolve manually with the dolt CLI (dolt_conflicts) and re-run flint sync pull"

// Remote — dolt_remotes 행 1건. URL은 원문 그대로 보관하되 노출은
// 반드시 MaskRemoteURL 경유(호출부 CLI 출력 계약).
type Remote struct {
	Name string
	URL  string
}

// RemoteAdd — 원격 등록. 중복명은 dolt_remote 실행 없이 ErrRemoteExists
// (실측: dolt_remote는 중복명을 조용히 덮어쓴다 — 사용자 의도와 다른
// 원격 교체를 원천 차단). 오류 래핑에 url 원문을 실지 않는다.
func RemoteAdd(ctx context.Context, q Querier, name, remoteURL string) error {
	rows, err := q.Query(ctx, SQLRemoteExists, name)
	if err != nil {
		return fmt.Errorf("sync remote add: check existing: %w", err)
	}
	if len(rows) > 0 {
		return fmt.Errorf("%w: %s", ErrRemoteExists, name)
	}
	if _, err := q.Query(ctx, SQLRemoteAdd, name, remoteURL); err != nil {
		// url을 인자로 전달하므로 방어적으로 원인을 보존하되 url 원문은
		// 이 래퍼에서 재기록하지 않는다(하위 오류 메시지는 dolt_remote
		// 표준 문구 — slow 실측에서 url 미포함 확인).
		return fmt.Errorf("sync remote add: dolt_remote: %w", err)
	}
	return nil
}

// RemoteList — 등록된 원격 전체(이름순). url은 원문 회수 — 마스킹은
// 출력 경로(CLI) 소관, 원천은 보존한다(push/pull 재사용).
func RemoteList(ctx context.Context, q Querier) ([]Remote, error) {
	rows, err := q.Query(ctx, SQLRemoteList)
	if err != nil {
		return nil, fmt.Errorf("sync remote list: %w", err)
	}
	remotes := make([]Remote, 0, len(rows))
	for _, row := range rows {
		if len(row) < 2 {
			return nil, fmt.Errorf("sync remote list: unexpected row arity %d", len(row))
		}
		remotes = append(remotes, Remote{
			Name: colText(row[0]),
			URL:  colText(row[1]),
		})
	}
	return remotes, nil
}

// RemoteRemove — 원격 삭제. 미존재명은 dolt_remote 실행 없이
// ErrRemoteNotFound(실측: dolt_remote는 "unknown remote" 오류를 내지만
// 사전 조회가 오류 계약을 안정화한다).
func RemoteRemove(ctx context.Context, q Querier, name string) error {
	rows, err := q.Query(ctx, SQLRemoteExists, name)
	if err != nil {
		return fmt.Errorf("sync remote remove: check existing: %w", err)
	}
	if len(rows) == 0 {
		return fmt.Errorf("%w: %s", ErrRemoteNotFound, name)
	}
	if _, err := q.Query(ctx, SQLRemoteRemove, name); err != nil {
		return fmt.Errorf("sync remote remove: dolt_remote: %w", err)
	}
	return nil
}

// MaskRemoteURL — 원격 url 마스킹: 스킴+호스트만 노출(자격증명·포트·
// 경로·쿼리 전부 미노출). file://처럼 호스트 없는 스킴은 "scheme://"만.
// 파싱 불가·스킴 부재 원문은 고정 토큰으로 대체해 원문을 절대 되돌리지
// 않는다(개인 데이터 방어 — Review Focus security).
func MaskRemoteURL(raw string) string {
	const unparsable = "(unparsable)"
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || strings.ContainsAny(raw, " \t\r\n") {
		return unparsable
	}
	host := u.Hostname() // userinfo·포트 제거
	if host == "" {
		return u.Scheme + "://"
	}
	return u.Scheme + "://" + host
}

// SyncResult — push/pull 결과 1건(JSON 출력 원천). Hash는 main head.
type SyncResult struct {
	Status string
	Hash   string
}

// SyncPush — dolt_push(remote, 'main') 실행. 미등록 원격은 dolt_push 실행
// 없이 ErrRemoteNotFound(실측: dolt_push의 미등록 오류는 "fatal: remote
// 'x' not found" — 사전 조회가 오류 계약을 안정화). 실행 오류(도달 불가
// 등)는 원인을 보존하되 메시지에 url 원문이 실리면 마스킹으로 치환한다.
func SyncPush(ctx context.Context, q Querier, remote string) (SyncResult, error) {
	rawURL, err := syncRemoteURL(ctx, q, remote)
	if err != nil {
		return SyncResult{}, err
	}
	if _, err := q.Query(ctx, SQLSyncPush, remote); err != nil {
		return SyncResult{}, maskRemoteError(fmt.Errorf("sync push: dolt_push: %w", err), rawURL)
	}
	hash, err := syncHeadHash(ctx, q)
	if err != nil {
		return SyncResult{}, err
	}
	return SyncResult{Status: "pushed", Hash: hash}, nil
}

// SyncPull — dolt_fetch(remote,'main') + dolt_merge(<remote>/main). dry는
// fetch까지만(적용 없음). merge 충돌은 ErrMergeConflict + 수동 해결 안내
// (실측: 충돌 시 @autocommit 트랜잭션이 롤백돼 작업집합이 보존된다 —
// 자동 해결도·부분 적용도 없다). 해시 전후 비교로 merged/up_to_date 구분.
func SyncPull(ctx context.Context, q Querier, remote string, dry bool) (SyncResult, error) {
	rawURL, err := syncRemoteURL(ctx, q, remote)
	if err != nil {
		return SyncResult{}, err
	}
	if _, err := q.Query(ctx, SQLSyncFetch, remote, "main"); err != nil {
		return SyncResult{}, maskRemoteError(fmt.Errorf("sync pull: dolt_fetch: %w", err), rawURL)
	}
	if dry {
		hash, err := syncHeadHash(ctx, q)
		if err != nil {
			return SyncResult{}, err
		}
		return SyncResult{Status: "fetched", Hash: hash}, nil
	}
	before, err := syncHeadHash(ctx, q)
	if err != nil {
		return SyncResult{}, err
	}
	if _, err := q.Query(ctx, SQLSyncMerge, remote+"/main"); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "conflict") {
			return SyncResult{}, fmt.Errorf("%w: %s (cause: %v)", ErrMergeConflict, MergeConflictGuide, err)
		}
		return SyncResult{}, maskRemoteError(fmt.Errorf("sync pull: dolt_merge: %w", err), rawURL)
	}
	after, err := syncHeadHash(ctx, q)
	if err != nil {
		return SyncResult{}, err
	}
	status := "up_to_date"
	if after != before {
		status = "merged"
	}
	return SyncResult{Status: status, Hash: after}, nil
}

// syncRemoteURL — 원격명으로 등록 url 회수. 미등록은 ErrRemoteNotFound.
// url은 마스킹 치환 원문으로만 쓰고 반환값 자체를 노출하지 않는다.
func syncRemoteURL(ctx context.Context, q Querier, remote string) (string, error) {
	rows, err := q.Query(ctx, SQLSyncRemoteURL, remote)
	if err != nil {
		return "", fmt.Errorf("sync: lookup remote: %w", err)
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("%w: %s", ErrRemoteNotFound, remote)
	}
	if len(rows[0]) < 2 {
		return "", fmt.Errorf("sync: lookup remote: unexpected row arity %d", len(rows[0]))
	}
	return colText(rows[0][1]), nil
}

// syncHeadHash — main 브랜치 head 해시(0행·파손 arity는 오류).
func syncHeadHash(ctx context.Context, q Querier) (string, error) {
	rows, err := q.Query(ctx, SQLSyncHeadHash)
	if err != nil {
		return "", fmt.Errorf("sync: head hash: %w", err)
	}
	if len(rows) == 0 || len(rows[0]) < 1 {
		return "", fmt.Errorf("sync: head hash: main branch not found")
	}
	return colText(rows[0][0]), nil
}

// maskRemoteError — 오류 메시지에서 원격 url 원문을 마스킹 결과로 치환
// (서버 오류가 url을 그대로 실을 수 있어 — push 성공 메시지 실측 기준).
// 치환 시 오류 체인은 끊기지만 이 경로는 종단 exit 1 오류로 체인 소비자가
// 없다(url 미노출 계약이 우선 — Review Focus security).
func maskRemoteError(err error, rawURL string) error {
	if rawURL == "" || !strings.Contains(err.Error(), rawURL) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), rawURL, MaskRemoteURL(rawURL)))
}

// colText — 셀 값을 text로 정규화(pgx text는 string이지만 [][]any 경유
// 임의형 방어 — fmt.Sprint 폴백).
func colText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

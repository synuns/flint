package store

import (
	"encoding/json"
	"errors"
)

// exit 판정 기준 에러 심볼. db 계층(T2 pool·T7 repo)은 이 값을 그대로
// 사용하거나(errors.Is로 통과) 자체 심볼을 별칭 지정한다.
var (
	// ErrServerDown — 즉시 실패, exit 2.
	ErrServerDown = errors.New("flint: server down")
	// ErrRetriesExhausted — 재시도 소진, exit 1.
	ErrRetriesExhausted = errors.New("flint: retries exhausted")
	// ErrDuplicate — content_hash 중복, exit 0(멱등).
	ErrDuplicate = errors.New("flint: duplicate")
)

// committed 출력 바디. 한 줄 JSON, 키 순서는 status·hash·state·id로 고정.
type committedOutput struct {
	Status string `json:"status"`
	Hash   string `json:"hash"`
	State  string `json:"state"`
	ID     int    `json:"id"`
}

// duplicateOutput — id>0일 때만 id 필드를 노출한다(R3: 기존 행 id.
// id 0(미지원 Store·다중 줄)은 맨몸 {"status":"duplicate"} 불변).
type duplicateOutput struct {
	Status string `json:"status"`
	ID     int    `json:"id,omitempty"`
}

// Classify는 쓰기 판정 규칙: kind==knowledge && confidence<floor → pending,
// 그 외(경계 포함)는 active. floor는 호출자가 meta conf_floor에서 읽어 값으로
// 전달한다(전역 주입 없음 — doneThresholds 관례). 범위 검사는 파서
// (ParseThresholds, 0~1 수용)가 단일 출처다.
func Classify(kind string, conf, floor float64) string {
	if kind == "knowledge" && conf < floor {
		return "pending"
	}
	return "active"
}

// OutputJSON은 파이프라인 출력을 한 줄 JSON으로 직렬화한다.
// status=="duplicate"면 status·(id>0인 경우)id만 출력하고 나머지 필드는 버린다.
func OutputJSON(status, hash, state string, id int) string {
	if status == "duplicate" {
		b, err := json.Marshal(duplicateOutput{Status: status, ID: id})
		if err != nil {
			return `{"status":"duplicate"}`
		}
		return string(b)
	}
	b, err := json.Marshal(committedOutput{Status: status, Hash: hash, State: state, ID: id})
	if err != nil {
		return `{"status":"` + status + `"}`
	}
	return string(b)
}

// MapExit는 파이프라인 에러를 exit code로 매핑한다.
// nil/중복→0, 재시도 소진→1, 서버 다운→2, 검증 실패→3, 그 외→1.
func MapExit(err error) int {
	var se *SchemaError
	switch {
	case err == nil, errors.Is(err, ErrDuplicate):
		return 0
	case errors.Is(err, ErrRetriesExhausted):
		return 1
	case errors.Is(err, ErrServerDown):
		return 2
	case errors.As(err, &se):
		return 3
	default:
		return 1
	}
}

// MultiCommitOutput — JSONL 다중(2줄 이상) 커밋 출력 바디. 줄별 hash·state·id
// 혼합 대신 집계(inserted·duplicates)와 배치 dolt 커밋 해시 1개만 남긴다
// (출력 계약 소유는 store 계층 — committedOutput 옆).
func MultiCommitOutput(inserted, duplicates int, commitHash string) string {
	b, err := json.Marshal(map[string]any{
		"status":     "committed",
		"inserted":   inserted,
		"duplicates": duplicates,
		"hash":       commitHash,
	})
	if err != nil {
		return `{"status":"committed"}`
	}
	return string(b)
}

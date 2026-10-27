package store

import (
	"encoding/json"
	"errors"
)

// 판정 경계: knowledge의 confidence가 이 값 미만이면 pending.
// 기본은 thresholds.go의 defaultConfFloor(단일 출처)이며, meta의 conf_floor를
// 읽은 호출자(cli newRealStore)가 SetClassifyThresholds로 주입한다
// (전역 제약: 임계값 하드코딩 금지 → conf_floor 소비).
var classifyFloor = defaultConfFloor

// SetClassifyThresholds — Classify 판정 기준을 meta에서 읽은 임계값으로 주입.
// 무효 ConfFloor(≤0, >1)는 거부해 현재 값을 유지 — meta 부재·조회 실패 시
// 기본 0.8 불변(하위호환).
func SetClassifyThresholds(th Thresholds) {
	if th.ConfFloor <= 0 || th.ConfFloor > 1 {
		return
	}
	classifyFloor = th.ConfFloor
}

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

type duplicateOutput struct {
	Status string `json:"status"`
}

// Classify는 쓰기 판정 규칙: kind==knowledge && confidence<conf_floor → pending,
// 그 외(경계 포함)는 active. conf_floor는 미주입 시 기본 0.8.
func Classify(kind string, conf float64) string {
	if kind == "knowledge" && conf < classifyFloor {
		return "pending"
	}
	return "active"
}

// OutputJSON은 파이프라인 출력을 한 줄 JSON으로 직렬화한다.
// status=="duplicate"면 {"status":"duplicate"}만 출력하고 나머지 필드는 버린다.
func OutputJSON(status, hash, state string, id int) string {
	if status == "duplicate" {
		b, err := json.Marshal(duplicateOutput{Status: status})
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

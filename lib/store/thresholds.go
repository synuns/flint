package store

import (
	"fmt"
	"log"
	"strconv"
)

// Thresholds는 meta 테이블에서 읽은 임계값 상수(전역 제약: 하드코딩 금지).
type Thresholds struct {
	PendingMax    int
	DupSuspMax    int
	ConfFloor     float64
	StaleSessions int
}

// 기본값 — pending>50·중복의심>20·conf<0.8·연속 3세션.
const (
	defaultPendingMax    = 50
	defaultDupSuspMax    = 20
	defaultConfFloor     = 0.8
	defaultStaleSessions = 3
)

// DefaultThresholds는 전 키 기본값 구조체.
func DefaultThresholds() Thresholds {
	return Thresholds{
		PendingMax:    defaultPendingMax,
		DupSuspMax:    defaultDupSuspMax,
		ConfFloor:     defaultConfFloor,
		StaleSessions: defaultStaleSessions,
	}
}

// ParseThresholds는 meta 테이블 키-값 읽기 결과(rows)를 Thresholds로 변환한다.
// 미설정 키는 기본값, 잘못된 값은 기본값+경고(에러 아님).
// e.g. ParseThresholds([][2]any{{"pending_max","50"},{"conf_floor","0.8"}}) -> {50,20,0.8,3}
func ParseThresholds(rows [][2]any) (Thresholds, error) {
	th := DefaultThresholds()
	for _, row := range rows {
		key, val := row[0], row[1]
		ks, ok := key.(string)
		if !ok {
			return th, fmt.Errorf("thresholds: 키가 문자열 아님: %v", key)
		}
		switch ks {
		case "pending_max":
			th.PendingMax = parseIntOrDefault(val, defaultPendingMax, ks)
		case "dup_susp_max":
			th.DupSuspMax = parseIntOrDefault(val, defaultDupSuspMax, ks)
		case "conf_floor":
			th.ConfFloor = parseFloatOrDefault(val, defaultConfFloor, ks)
		case "stale_sessions":
			th.StaleSessions = parseIntOrDefault(val, defaultStaleSessions, ks)
		}
	}
	return th, nil
}

// toString은 any 값을 문자열로 정규화한다(숫자 타입 수용).
func toString(val any) (string, bool) {
	switch v := val.(type) {
	case string:
		return v, true
	case int:
		return strconv.Itoa(v), true
	case int64:
		return strconv.FormatInt(v, 10), true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	default:
		return "", false
	}
}

// parseIntOrDefault는 정수 파싱 실패 시 기본값+경고.
func parseIntOrDefault(val any, def int, key string) int {
	s, ok := toString(val)
	if !ok {
		log.Printf("[flint] thresholds: %s 값 타입 이상(%v) — 기본값 %d", key, val, def)
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		log.Printf("[flint] thresholds: %s=%q 파싱 불가 — 기본값 %d", key, s, def)
		return def
	}
	return n
}

// parseFloatOrDefault는 실수 파싱 실패 시 기본값+경고.
func parseFloatOrDefault(val any, def float64, key string) float64 {
	s, ok := toString(val)
	if !ok {
		log.Printf("[flint] thresholds: %s 값 타입 이상(%v) — 기본값 %v", key, val, def)
		return def
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || f > 1 {
		log.Printf("[flint] thresholds: %s=%q 파싱 불가 — 기본값 %v", key, s, def)
		return def
	}
	return f
}

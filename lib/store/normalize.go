package store

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// Normalize는 kind 입력값을 저장 규격으로 정규화한 새 맵을 반환한다.
// tags []string → 정렬 후 ":a:b:" 단일 문자열, 타임스탬프는 UTC,
// knowledge의 domain·origin 기본값과 valid_at 미지정 시 now를 채운다.
func Normalize(kind string, p map[string]any) map[string]any {
	out := make(map[string]any, len(p)+3)
	for k, v := range p {
		out[k] = v
	}
	if kind == "knowledge" {
		if tags, ok := out["tags"]; ok && tags != nil {
			out["tags"] = normalizeTags(tags)
		}
		if s, ok := out["domain"].(string); !ok || s == "" {
			out["domain"] = "main"
		}
		if s, ok := out["origin"].(string); !ok || s == "" {
			out["origin"] = "agent"
		}
		if v, ok := out["valid_at"]; !ok || v == nil {
			out["valid_at"] = time.Now().UTC().Format(time.RFC3339Nano)
		} else {
			out["valid_at"] = toUTC(v)
		}
	}
	if v, ok := out["ts"]; ok {
		out["ts"] = toUTC(v)
	}
	return out
}

// normalizeTags — 정렬 후 ":a:b:" 형식으로. 빈 태그면 빈 문자열.
func normalizeTags(v any) string {
	var tags []string
	switch t := v.(type) {
	case []string:
		tags = t
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok {
				tags = append(tags, s)
			}
		}
	default:
		return ""
	}
	if len(tags) == 0 {
		return ""
	}
	sort.Strings(tags)
	return ":" + strings.Join(tags, ":") + ":"
}

// toUTC — RFC3339 문자열을 UTC로 정규화. 파싱 불가면 원값 그대로.
func toUTC(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return v
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// CanonicalJSON은 키 정렬 직렬화해 해시 안정성을 보장한다.
// e.g. CanonicalJSON({"b":1,"a":2}) -> `{"a":2,"b":1}`
func CanonicalJSON(p map[string]any) string {
	b, err := json.Marshal(p) // encoding/json은 map 키를 정렬한다
	if err != nil {
		return ""
	}
	return string(b)
}

// hashVolatile — 해시 계산 시 제외하는 휘발성 필드. valid_at은 Normalize가
// 미지정 시 now로 주입하고 ts는 시각 그 자체라, 해시에 포함하면 동일 payload의
// 재전송(시딩 멱등)이 duplicate로 판정되지 않는다.
var hashVolatile = map[string]bool{"valid_at": true, "ts": true}

// HashCanonical은 휘발성 필드(valid_at, ts)를 제외한 canonical JSON을 반환한다.
// ContentHash의 입력은 반드시 이 경로를 거쳐야 한다.
func HashCanonical(p map[string]any) string {
	m := make(map[string]any, len(p))
	for k, v := range p {
		if hashVolatile[k] {
			continue
		}
		m[k] = v
	}
	return CanonicalJSON(m)
}

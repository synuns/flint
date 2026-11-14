// import_pam.go — v1.3.0 T10: PAM(Portable AI Memory) v1.0 읽기.
// 외부 PAM 내보내기 문서를 flint 임포트 후보로 변환하는 순수 파서 —
// 스토어 무접촉(계약 5): pending 적립 정책·실제 write는 T9
// (import_migrate.go) 소유. T9는 seam 변수 ParsePAMFn으로 연결한다.
package knowledge

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ImportCandidate — PAM memory 1건의 flint 변환 결과. ExportRow 호환
// 필드명(왕복 jsonl export·import 대칭)이며 pending 적립 대상
// (status active)만 들어간다.
type ImportCandidate struct {
	ID         string         `json:"id"` // 원 PAM memory id(계보 갱신 연결용)
	Kind       string         `json:"kind"`
	Content    string         `json:"content"`
	Tags       []string       `json:"tags,omitempty"`
	Confidence float64        `json:"confidence"`
	Source     string         `json:"source"` // "import:pam:"+platform
	CreatedAt  string         `json:"created_at,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// PAMSkipped — 적립 대상이 아닌 status의 memory(보고용 — T9 출력).
type PAMSkipped struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"` // superseded/deprecated/retracted/archived
}

// SupersedePair — relations 중 type=="supersedes"의 (from,to). from이
// to를 대체한다는 계보 — 후처리 갱신은 T9 소유.
type SupersedePair struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// PAMResult — ParsePAM 반환 묶음.
type PAMResult struct {
	Candidates     []ImportCandidate
	Skipped        []PAMSkipped
	SupersedePairs []SupersedePair
}

// ParsePAMFn — T9 소비 seam(테스트 치환 가능).
var ParsePAMFn = ParsePAM

// PAM v1.0 문서 구조(필요 최소 필드만 — summary는 무시: content 우선).
type pamDoc struct {
	Schema    string        `json:"schema"`
	Memories  []pamMemory   `json:"memories"`
	Relations []pamRelation `json:"relations"`
}

type pamMemory struct {
	ID         string        `json:"id"`
	Type       string        `json:"type"`
	Content    string        `json:"content"`
	Temporal   pamTemporal   `json:"temporal"`
	Status     string        `json:"status"`
	Confidence pamConfidence `json:"confidence"`
	Tags       []string      `json:"tags"`
	Provenance pamProvenance `json:"provenance"`
}

type pamTemporal struct {
	CreatedAt string `json:"created_at"`
}

// Current 포인터 — 생략(0 역직렬화)과 명시 0을 구분해 기본값 0.5 적용.
type pamConfidence struct {
	Current *float64 `json:"current"`
}

type pamProvenance struct {
	Platform string `json:"platform"`
}

type pamRelation struct {
	Type string `json:"type"`
	From string `json:"from"`
	To   string `json:"to"`
}

// pamKindByType — PAM type → flint kind. 표준 knowledge형 9종 + project.
// custom(및 알 수 없는 type)는 여기 없다 — knowledge + Metadata["custom_type"]
// 원문 보존 경로로 떨어진다.
var pamKindByType = map[string]string{
	"fact":         "knowledge",
	"preference":   "knowledge",
	"skill":        "knowledge",
	"context":      "knowledge",
	"goal":         "knowledge",
	"instruction":  "knowledge",
	"environment":  "knowledge",
	"identity":     "knowledge",
	"relationship": "knowledge",
	"project":      "project",
}

// ParsePAM — PAM v1.0 문서(data)를 후보·스킵·supersedes 쌍으로 변환.
// 루트 schema 불일치·파손 json은 오류(파손은 offset 포함 상세).
func ParsePAM(data []byte) (*PAMResult, error) {
	var doc pamDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			return nil, fmt.Errorf("pam json 파손(offset %d): %w", syn.Offset, err)
		}
		var typ *json.UnmarshalTypeError
		if errors.As(err, &typ) {
			return nil, fmt.Errorf("pam json 필드 타입 오류(%s.%s offset %d): %w",
				typ.Struct, typ.Field, typ.Offset, err)
		}
		return nil, fmt.Errorf("pam json 해석 실패: %w", err)
	}
	if doc.Schema != "portable-ai-memory" {
		return nil, fmt.Errorf("pam schema 불일치: %q (want %q)", doc.Schema, "portable-ai-memory")
	}
	res := &PAMResult{}
	for _, m := range doc.Memories {
		if m.Status != "active" {
			// superseded/deprecated/retracted/archived — pending 적립
			// 대상 아님. 계약 명시 5종 외 알 수 없는 status도 조용한
			// 적립 대신 눈에 보이는 스킵 보고로 남긴다(원문 status 보존).
			res.Skipped = append(res.Skipped, PAMSkipped{ID: m.ID, Type: m.Type, Status: m.Status})
			continue
		}
		c := ImportCandidate{
			ID:         m.ID,
			Kind:       "knowledge",
			Content:    m.Content, // content 우선 — summary는 무시(계약 2)
			Tags:       m.Tags,
			Confidence: 0.5, // 생략 시 import 경로 기본(import.go 관례)
			Source:     "import:pam:" + m.Provenance.Platform,
			CreatedAt:  m.Temporal.CreatedAt,
		}
		if m.Confidence.Current != nil {
			c.Confidence = clampUnit(*m.Confidence.Current)
		}
		if k, ok := pamKindByType[m.Type]; ok {
			c.Kind = k
		} else {
			// custom — 원 custom_type 보존
			if c.Metadata == nil {
				c.Metadata = map[string]any{}
			}
			c.Metadata["custom_type"] = m.Type
		}
		res.Candidates = append(res.Candidates, c)
	}
	for _, r := range doc.Relations {
		if r.Type == "supersedes" {
			res.SupersedePairs = append(res.SupersedePairs, SupersedePair{From: r.From, To: r.To})
		}
	}
	return res, nil
}

// clampUnit — [0,1] 클램프(confidence.current).
func clampUnit(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

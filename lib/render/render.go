// render.go — 출력 살균 조크포인트(v0.38.0 패키지 분할 1/3: lib/cli에서
// 이동, 스펙 §2). 모든 출력 경로는 이 출구를 통해서만 store.SanitizeText·
// MaskSecrets에 도달한다(직접 호출은 TestSanitizeTextChokepoint가 차단).
package render

import (
	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/store"
)

// Text — 문자열 단위 조크포인트 가드(store.SanitizeText 위임).
// e.g. Text("a[flint]b") -> "a b"
func Text(s string) string {
	return store.SanitizeText(s)
}

// Masked — 마스킹 조크포인트 가드. 마스킹→살균 순서 보존(ADR-0015
// 계약): [REDACTED:…] 흔적은 중립화를 통과해 남는다.
func Masked(s string) string {
	return store.SanitizeText(store.MaskSecrets(s))
}

// MaskedStrict — 기기 밖(브리지) 전용: allowlist 등록값도 마스킹
// (v1.0.10 감사 B2 — 진단용 dump --mask의 Masked와 구분).
func MaskedStrict(s string) string {
	return store.SanitizeText(store.MaskSecretsStrict(s))
}

// Cells — 셀 단위 전수 살균. 문자열 셀만 중립화하고 비문자열 셀은
// 원값 유지(hot 의존성 — 원 슬라이스를 그대로 갱신 허용).
// e.g. Cells([][]any{{1, "x[flint]y"}}) -> [][]any{{1, "x y"}}
func Cells(rows [][]any) [][]any {
	for _, row := range rows {
		for i, cell := range row {
			if s, ok := cell.(string); ok {
				row[i] = store.SanitizeText(s)
			}
		}
	}
	return rows
}

// Rows — 행 포맷: Cells 전수 살균 후 query.FormatRows 위임.
func Rows(rows [][]any, fields string, jsonMode bool) string {
	return query.FormatRows(Cells(rows), fields, jsonMode)
}

// Package knowledge — flint 지식 유입·회상 커맨드 구현(v0.39.0 §2 1·2탄).
// write·import·read·hot 핸들러와 유입 계측(RecordUsage·RecordDups)·판정
// 임계값 로더(WriteThresholds)·살균(SanitizePayload) 공용 표면을 소유한다.
// 커맨드 등록은 init에서 wire.Register로 — cli는 이 패키지를 임포트하고
// (cli→knowledge 단방향 — knowledge가 cli를 임포트하면 사이클),
// 잔류 cli 소비자는 cli/knowledge_alias.go 소문자 var·타입 별칭으로
// 무편집 컴파일된다.
package knowledge

import "github.com/synuns/flint/lib/wire"

func init() {
	wire.Register("write", RunWrite)
	wire.Register("import", RunImportRouted) // v1.3.0 T9: --format 유무 분기(기본 RunImport 불변)
	wire.Register("read", RunRead)
	wire.Register("hot", RunHot)
}

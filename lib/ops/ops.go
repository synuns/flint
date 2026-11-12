// Package ops — flint 진단·정리·관측 커맨드 구현(v0.40.0 §1 1탄).
// doctor·audit·diff·status·tuning 핸들러와 진단 계열(체크 등록표·하니스
// 검진·임계값 인터페이스) 표면을 소유한다. 커맨드 등록은 init에서
// wire.Register로 — cli는 이 패키지를 임포트하고(cli→ops 단방향 — ops가
// cli를 임포트하면 사이클), 잔류 cli 소비자는 cli/ops_alias_1.go 소문자
// var·타입 별칭으로 무편집 컴파일된다. cli 소유 구현(버전·업데이트·모순
// 분류·스킬 검사·as-of 정규화 등)은 각 파일의 Fn seam으로 cli init이
// 주입한다(knowledge.ParseAsOfSpecFn과 같은 관례 — T2·T3·T4 이동 완료
// 후 직접 호출로 수렴).
package ops

import "github.com/synuns/flint/lib/wire"

func init() {
	wire.Register("doctor", RunDoctor)
	wire.Register("audit", runAudit)
	wire.Register("diff", RunDiff)
	wire.Register("status", RunStatus)
	wire.Register("tuning", RunTuning)
}

// Package ops — flint 진단·정리·관측 커맨드 구현(v0.40.0 §1 1·2·3탄).
// doctor·audit·diff·status·tuning(1탄)·maintain·metrics·marker·nudge·
// sessionhook(2탄)·report·report_html·backup·restore·sigverify·rules_sync
// (3탄) 핸들러와 진단·정리 계열(체크 등록표·하니스 검진·임계값
// 인터페이스·마커·계측) 표면을 소유한다. 커맨드 등록은 init에서
// wire.Register로 — cli는 이 패키지를 임포트하고(cli→ops 단방향 — ops가
// cli를 임포트하면 사이클), 잔류 cli 소비자는 cli/ops_alias_1·2·3.go 소문자
// var·타입 별칭으로 무편집 컴파일된다. backup·restore 등록은 아직
// cli(cliwire_alias init) 경유(동일 핸들러 이중 등록 무해 — T4 수렴).
package ops

import "github.com/synuns/flint/lib/wire"

func init() {
	wire.Register("doctor", RunDoctor)
	wire.Register("audit", runAudit)
	wire.Register("diff", RunDiff)
	wire.Register("status", RunStatus)
	wire.Register("tuning", RunTuning)
	wire.Register("maintain", RunMaintain)
	wire.Register("nudge", RunNudge)
	wire.Register("session-start", RunSessionStart)
	wire.Register("session-end", RunSessionEnd)
	wire.Register("report", RunReport)
}

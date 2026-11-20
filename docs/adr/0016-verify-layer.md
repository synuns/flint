# ADR-0016 — 런타임 검증 단(verify-flint 스킬) 도입

- **상태**: 채택(2026-11-18)
- **맥락**: 루프의 검증은 게이트(go test·e2e·리뷰)로 충실하지만 "실사용자
  경로를 돈 증거"가 없어, 릴리스 승인 시 사람이 무엇을 열람할지 표준이
  없었다. pstack 조사(2026-11-17)에서 검증 스킬·feature map·증거 보관은
  프로젝트 구축분으로 확인됨.
- **옵션**: ① 증거를 repo docs에 커밋 ② 증거 없이 게이트만 유지
  ③ 검증 스킬 + 로컬 증거 패킷(채택). ①은 런 로그 로컬 한정 원칙 위반,
  ②는 "사람은 최종 검증만" 목적 미달.
- **결정**: `.claude/skills/verify-flint/`(스킬·feature map — 커밋)과
  `.superlite/verify/<version>/`(증거 패킷 — 로컬 한정)를 둔다. 드라이브는
  합성 시드 전용 DB(flint_verify)만 사용. 증거 캡처는 tmux 텍스트
  스냅샷 표준(GIF·영상은 공개 데모 산출에만 선택). 사람 열람은
  릴리스(G3) 승인 시 manifest로. feature map은 마이너 버전마다
  maintain-verification-skill 패스로 드리프트를 정비한다(LOOP.md §4).
- **되돌림 트리거**: 증거 패킷이 릴리스 승인에서 2회 연속 열람되지 않거나,
  드라이브 유지비(maintain-verification-skill 갱신)가 기능 변경 대비
  과중해지면 커버리지를 축소하거나 폐기한다.

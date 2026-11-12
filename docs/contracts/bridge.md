# 브리지 계약 — 비로컬 AI 접근 (ADR-0015 요약·원격 쓰기 금지)

원천: ADR-0015(`docs/adr/0015-bridge-surface.md`)·`flint brief`.

## 계약

- 비로컬 AI(챗 비서 등)는 **브리지**로만 접근한다:
  `flint brief [query]`가 마스킹된 읽기 전용 블록을 내고, 사용자가
  임의의 채팅에 붙여넣어 전달한다(단방향 수출면).
- 유출 방어(기기 밖 문자열 전부): MaskSecretsStrict(비밀 패턴 치환 +
  allowlist 등록값 마스킹 — v1.0.11 패턴 우선·중첩 등록 내림차순) →
  SanitizeText(제어 문자·`[flint]` 마커 중립화). 조회 원천은
  active_knowledge 뷰로 한정 — pending·invalidated 미검수 지식은
  기기 밖으로 나가지 않는다.
- **회수는 pending 인박스 한정**: 비로컬 AI 산출물의 유입 경로는
  ① `flint write knowledge`(confidence < conf_floor → pending 분류)
  ② `flint import`(항상 pending)뿐이며, 기존 pending→approve 검수
  회로를 그대로 통과한다. 신뢰된 필자는 로컬 에이전트뿐이다.

## 경계

- flint 표면의 로컬 결합 4종(ADR-0015): 전송(stdio)·데이터
  (Doltgres localhost 바인딩, ADR-0010)·주입(로컬 하네스 훅)·신뢰
  (ADR-0013 로컬 단일 사용자). 비로컬 연결은 pull-only가 한계.

## 금지

- **원격 라이브 쓰기 경로 개방 — 어떤 형태(원격 MCP write·HTTP API·
  양방향 싱크)로도 pending을 건너뛰는 쓰기는 만들지 않는다.**
- 원격 MCP 표면 재론은 3조건 충족 시에만(브리지 실사용 관측·접근
  대상 특정·사용자 인프라 노출 감수) + ADR-0013 개정 선행.
- 브리지를 서버 제품화하는 방향 — flint의 정체성은 단일 Go 바이너리
  + 로컬 Doltgres다.

## 예시

```sh
flint brief "배포 파이프라인 결정"
# → 마스킹된 읽기 전용 [flint] 블록 — 붙여넣어 전달, 회수는 write/import → pending
```

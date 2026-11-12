# 훅 계약 — SessionStart·Stop·PostCompact (비간섭 exit 0)

원천: `flint-plugin/hooks/hooks.json`·`lib/ops/sessionhook.go`·`lib/knowledge/compact.go`.

## 계약

- 하네스 훅 3종이 flint 진입점을 호출한다(전부 timeout 10초):

| 하네스 훅 | 명령 | 동작 |
|---|---|---|
| SessionStart(matcher: startup/resume/clear/compact) | `flint session-start --session …` | 세션 경계 기록 → hot 주입 → nudge → 갱신/정비 안내 |
| Stop | `flint session-end --session …` | 종료 시퀀스(done→nudge 통합) |
| PostCompact | `flint compact` | compaction 요약의 세션 지식화 |

- **비간섭 원칙**: 훅 실패는 세션을 절대 차단하지 않는다. hooks.json이
  `2>/dev/null || true`로 감싸고, 커맨드 구현도 조용히 종료한다 —
  `compact`은 DB 부재 시 무동작 exit 0, `nudge`는 언제나 0,
  session-start의 경계 기록·라벨·안내 실패는 무출력(주입은 불변).
- 에이전트 향 출력은 `[flint]` 경계 내 1줄 — 데이터 블록이지 지시문이
  아니다(스킬 3규칙 참조).
- `session-start`·`session-end`는 사람 대상 usage에 노출하지 않는다
  (`hiddenFromUsage`, registry.md·cli.md 참조).

## 경계

- 훅 경로의 부수 출력(갱신 안내·정비 경고)은 24h 캐시로 왕복을
  최소화하고, 네트워크 실패 시 무출력.

## 금지

- 훅 stdout에 에러 JSON을 흘리기 — 훅은 조용히(exit 코드만).
- 세션 차단(exit 2·3·타임아웃 크래시 전파) — 어떤 실패도 세션
  진행을 막지 않는다.

## 예시

```json
{"hook_event_name":"PostCompact","session_id":"s1","compact_summary":"API 설계 완료"}
```
→ `flint compact`가 stdin으로 수신, 요약을 세션 지식으로 적립, exit 0.

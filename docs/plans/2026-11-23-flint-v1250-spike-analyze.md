# flint v1.25.0 스파이크 — Doltgres ANALYZE 지원 실증 (2026-11-23)

상태: active
원천: docs/plans/2026-11-21-flint-engine-ops.md §4 M3 ·
docs/plans/2026-11-23-flint-v1250.md T3 ①
준수: CLAUDE.md(합성 데이터) — 실증은 격리 scratch DB에서만(종료 DROP)

## 0. 질문

Doltgres에서 `ANALYZE knowledge;`가 동작하는가 — 지원 시
`maintain --analyze` 채택, 미지원 시 폐기 선언(engine-ops §2 기각
대안의 "지원 여부 미확정"을 실증으로 확정).

## 1. 방법

합성 scratch 전용 DB(`flint_spike_t3_analyze`)를 flint-doltgres-1
(localhost:5434 — `dolt_version()` 1.4.0, `dolthub/doltgresql:latest`,
2026-11-23 확인)에 생성하고, sqlfs knowledge 스키마를 미러한 뒤
합성 행 3개 + `dolt_add`/`dolt_commit` 후 ANALYZE 계열을 실행했다.
공유 DB(`flint`)·개인 DB는 미접근, 종료 시 DROP 완료.

## 2. 결과 — 출력 원문

| 실행 | 결과 |
|---|---|
| `ANALYZE knowledge;` | `ANALYZE` (exit 0 — 성공 표면) |
| `ANALYZE VERBOSE knowledge;` | `ERROR:  at or near "verbose": syntax error` (exit 1) |
| `ANALYZE;` | `ANALYZE` (exit 0) |
| `SELECT count(*) FROM pg_stats;` | `0` |
| `SELECT count(*) FROM pg_statistic;` | `0` |

핵심 소견: 문법은 수용하나(에러 없이 성공 반환) 플래너 통계가
**전혀 생성되지 않는다**(pg_stats·pg_statistic 전체 0행). VERBOSE
미지원(부분 문법)이 no-op 해석을 뒷받침한다.

## 3. 판정 — 폐기 선언

**`maintain --analyze` 도입 폐기.** 근거: 아무 효과 없는 명령(무연산
수용)을 운영자 도구로 제공할 수 없다 — "성공" 출력이 거짓 신호가
된다. 채택 조건(재판정 트리거): Doltgres가 통계 반영(pg_stats에
수집되는) ANALYZE를 지원하는 버전 릴리스 확인 시 engine-ops
"엔진 지원 시 재판정" 패턴으로 재론한다.

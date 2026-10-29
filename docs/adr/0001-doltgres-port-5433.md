# ADR-0001: Doltgres 호스트 포트 = 5433

> **superseded by ADR-0010** (2026-10-29) — 외부 컨테이너의 5433 점유로
> 호스트 포트를 5434로 이전. 본 문서는 역사 기록으로 보존.

- **상태**: 채택 (2026-10-21, W0 스파이크) → superseded by ADR-0010
- **맥락**: PLAN.md §4는 "표준 포트 5432"를 명시. 그러나 호스트의 로컬 Postgres(PID 916,
  Homebrew)가 localhost:5432를 선점 중 — Docker의 *:5432 바인딩이 루프백에서 패배해
  psql이 계속 로컬 Postgres로 접속됨(스파이크 3회 실패의 원인).
- **결정**: 컨테이너 내부는 5432 유지, 호스트 노출은 **5433**. 접속 문자열
  `postgresql://flint:flint-dev@localhost:5433/flint`. docker-compose·launchd·문서 전부 5433.
- **되돌림 트리거**: 로컬 Postgres 제거 시 5432 복귀 가능(마이그레이션 불필요, 접속 문자열만 교체).

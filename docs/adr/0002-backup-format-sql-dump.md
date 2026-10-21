# ADR-0002: 백업 포맷 = flint 자체 SQL 덤프 + 데이터디렉터리 스냅샷

- **상태**: 채택 (2026-10-21, W0 스파이크)
- **맥락**: PLAN.md §15 "백업 포맷 확정: pg_dump 검증 결과에 따름".
- **결과**: pg_dump FAIL — Doltgres 1.4.0이 `SET TRANSACTION ISOLATION LEVEL
  REPEATABLE READ, READ ONLY` 미지원. `dolt_backup('sync-to', ...)`도 미지원.
- **결정**: `flint backup`은 ①flint 엔진이 직접 생성하는 SQL 덤프(스키마 DDL + INSERT
  재생 스크립트, `flint restore --file`로 복구) ②정지 시점 데이터디렉터리
  (/var/lib/doltgres) 카피 — 이중화. AS OF 시간여행은 동작하므로 논리 백업이
  커밋 단위로 재생 가능함을 전제로 한다.
- **되돌림 트리거**: Doltgres 상위 버전이 pg_dump를 지원하면 재평가.

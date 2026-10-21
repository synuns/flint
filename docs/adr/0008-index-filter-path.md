# ADR-0008: 검색 인덱스는 필터 경로만 — %q% 무료텍스트는 시퀀셜 수용

- 상태: 채택 (2026-10-22, v0.5 T51)
- 맥락: knowledge 검색에서 entity·domain·status 필터와 summary/topic %q% 무료텍스트 조회가 함께 쓰인다. B-tree는 중접두사(leading-wildcard) %q% 패턴에 인덱스를 쓸 수 없다.
- 결정: 필터 경로 3종만 B-tree 인덱스로 제공한다 — `flint_knowledge_entity_idx(entity)`, `flint_knowledge_domain_idx(domain)`, `flint_knowledge_status_idx(status)` (CREATE INDEX IF NOT EXISTS, meta.schema_version=3). %q% 무료텍스트는 시퀀셜 스캔을 수용한다. 대안 기각: pg_trgm GIN(표준 범위 밖 stdlib+pgx 제약, Doltgres 미검증), 함수 인덱스(스파이크 2026-10-22 동작 확인했으나 무료텍스트 패턴엔 부적합).
- 근거: 2026-10-22 스파이크 — Doltgres 5433에서 CREATE INDEX·함수 인덱스 동작 확인. 필터 경로가 카디널리티를 줄여 무료텍스트 스캔 범위도 줄어든다.
- 되돌림 트리거: 무료텍스트 조회가 실측 병목으로 확인되면 pg_trgm/FTS 도입을 별도 ADR로 재검토한다.

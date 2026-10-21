# ADR-0004: session.tasks·project.note 스키마 수용

- 상태: 채택 (2026-10-21, v0.2 T6)
- 맥락: v0.1이 스키마 미정의 키를 경고 후 제거(warnings 고지).
- 결정: 컬럼 추가 수용(sessions.tasks text, projects.note text, meta.schema_version=2). 대안(경고 유지·별도 메타 테이블) 기각 — 스펙 명시 키의 영구 경고는 부채.
- 되돌림 트리거: 사용 빈도 3개월 미만 시 컬럼 제거 검토.

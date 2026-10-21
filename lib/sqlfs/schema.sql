-- flint 스키마 (spec §6 데이터모델, meta.schema_version=3)
-- v2(ADR-0004): sessions.tasks·projects.note 추가. 기존 v1 DB는
-- admin.go의 버전 게이트가 ALTER로 적용한다(repo.go는 v1 소유 수정 금지).
-- v3(ADR-0008): knowledge 필터 경로(entity·domain·status) 인덱스 3종 추가.
-- %q% 무료텍스트는 B-tree가 불가해 시퀀셜 스캔을 수용한다.
-- 포트 5433 Doltgres 전용(ADR-0001). write는 INSERT 전용(projects 제외 UPDATE 금지).

CREATE TABLE IF NOT EXISTS projects (
    id      serial PRIMARY KEY,
    name    text NOT NULL UNIQUE,
    status  text NOT NULL DEFAULT 'active',  -- active|paused|done
    updated timestamptz NOT NULL DEFAULT now(),
    note    text                               -- v2: 프로젝트 메모(선택)
);

CREATE TABLE IF NOT EXISTS sessions (
    id      serial PRIMARY KEY,
    ts      timestamptz NOT NULL DEFAULT now(),
    agent   text NOT NULL,
    summary text NOT NULL,
    tasks   text                               -- v2: 세션 작업 목록(선택)
);

CREATE TABLE IF NOT EXISTS knowledge (
    id           serial PRIMARY KEY,
    topic        text NOT NULL,
    summary      text NOT NULL,
    tags         text NOT NULL DEFAULT '',   -- ':a:b:' 정규화 형식
    entity       text,
    domain       text NOT NULL DEFAULT 'main',
    confidence   double precision NOT NULL,
    origin       text NOT NULL DEFAULT 'agent',  -- agent|human
    status       text NOT NULL DEFAULT 'active', -- pending|active
    content_hash text NOT NULL UNIQUE,
    session_id   integer REFERENCES sessions(id),
    valid_at     timestamptz NOT NULL DEFAULT now(),
    invalid_at   timestamptz,
    ingested_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS maintenance_runs (
    id        serial PRIMARY KEY,
    ts        timestamptz NOT NULL DEFAULT now(),
    kind      text NOT NULL,
    details   text
);

CREATE TABLE IF NOT EXISTS meta (
    key   text PRIMARY KEY,
    value text NOT NULL
);

-- v3(ADR-0008): 검색 필터 경로 인덱스 — 신규 DB 생성 시점에 포함.
CREATE INDEX IF NOT EXISTS flint_knowledge_entity_idx ON knowledge (entity);
CREATE INDEX IF NOT EXISTS flint_knowledge_domain_idx ON knowledge (domain);
CREATE INDEX IF NOT EXISTS flint_knowledge_status_idx ON knowledge (status);

-- flint 스키마 (spec §6 데이터모델, meta.schema_version=1)
-- 포트 5433 Doltgres 전용(ADR-0001). write는 INSERT 전용(projects 제외 UPDATE 금지).

CREATE TABLE IF NOT EXISTS projects (
    id      serial PRIMARY KEY,
    name    text NOT NULL UNIQUE,
    status  text NOT NULL DEFAULT 'active',  -- active|paused|done
    updated timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sessions (
    id      serial PRIMARY KEY,
    ts      timestamptz NOT NULL DEFAULT now(),
    agent   text NOT NULL,
    summary text NOT NULL
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

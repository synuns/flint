-- flint 스키마 (spec §6 데이터모델, meta.schema_version=3 — v4 게이트가 4로 승격)
-- v2(ADR-0004): sessions.tasks·projects.note 추가. 기존 v1 DB는
-- admin.go의 버전 게이트가 ALTER로 적용한다(repo.go는 v1 소유 수정 금지).
-- v3(ADR-0008): knowledge 필터 경로(entity·domain·status) 인덱스 3종 추가.
-- %q% 무료텍스트는 B-tree가 불가해 시퀀셜 스캔을 수용한다.
-- v4(spec §2): knowledge 참조 계측 read_count·last_read_at 추가. 기존 v3 DB는
-- admin.go의 EnsureSchemaV4 게이트가 ALTER로 적용한다(신규 인덱스 없음).
-- v5(spec v0.8 §2): knowledge 계보 superseded_by 추가(배치 무효화가 대체 지식
-- id를 새긴다, 기본 NULL). 기존 v4 DB는 EnsureSchemaV5 게이트가 적용.
-- v6(v0.19.0 docs/METRICS.md): 효과 계측 이벤트 로그 read_events 추가(신규
-- 테이블 — 기존 테이블 무변경). 기존 v5 DB는 EnsureSchemaV6 게이트가 적용.
-- v7(v0.20.0): 효과 계측 정밀화 — read_events.ids(반환 지식 id CSV)·
-- session_starts(세션 경계)·hot_events(주입 노출) 추가. 기존 v6 DB는
-- EnsureSchemaV7 게이트가 적용(ALTER + CREATE 2종, additive).
-- views는 무변경 — 컬럼이 기존 뷰 정의에 영향 없다.
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
    ingested_at  timestamptz NOT NULL DEFAULT now(),
    read_count   integer NOT NULL DEFAULT 0,  -- v4: 참조 횟수(신규 인덱스 없음)
    last_read_at timestamptz,                 -- v4: 최근 참조 시각
    superseded_by int                         -- v5: 대체 지식 id(기본 NULL)
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

-- v6: read 1회당 1행 효과 계측 이벤트(G4 — docs/METRICS.md §4).
-- 개인 문구·쿼리 본문 없이 수치·플래그·세션 라벨만. 14일 보존
-- (daily 카운터와 동일 정책 — 롤오버 시 PruneReadEvents 정리).
-- v7 ids: 반환된 지식 id CSV(상위 10 — hit 전수는 hits가 유지).
CREATE TABLE IF NOT EXISTS read_events (
    id        serial PRIMARY KEY,
    ts        timestamptz NOT NULL DEFAULT now(),
    hits      integer NOT NULL,             -- 반환 행 수
    zero      boolean NOT NULL,             -- 결과 0(read_zero 분류)
    near      boolean NOT NULL,             -- 제로근접(read_zero_near 분류)
    fallback  boolean NOT NULL,             -- 폴백 재쿼리 경유
    session   text NOT NULL DEFAULT '',     -- 호출자 세션 라벨(선택)
    ids       text NOT NULL DEFAULT ''      -- 반환 지식 id CSV(v0.20.0, 상위 10)
);

-- v7: 세션 시작 경로 1행(SessionStart 훅 — session-start 커맨드가 기록).
-- 세션 구간 [start_i, start_{i+1}) 배정의 원천. compact·resume 재발발은
-- 같은 세션의 보수적 분할로 수용(관례 — METRICS §4).
CREATE TABLE IF NOT EXISTS session_starts (
    id      serial PRIMARY KEY,
    ts      timestamptz NOT NULL DEFAULT now(),
    session text NOT NULL DEFAULT ''
);

-- v7: hot --full 주입 1행(실제 출력에 남은 지식 id만 — 노출 계정).
-- dead_pct의 hot 노출 보정 원천(E — METRICS §4).
CREATE TABLE IF NOT EXISTS hot_events (
    id  serial PRIMARY KEY,
    ts  timestamptz NOT NULL DEFAULT now(),
    ids text NOT NULL DEFAULT ''            -- 주입 지식 id CSV
);

-- v3(ADR-0008): 검색 필터 경로 인덱스 — 신규 DB 생성 시점에 포함.
CREATE INDEX IF NOT EXISTS flint_knowledge_entity_idx ON knowledge (entity);
CREATE INDEX IF NOT EXISTS flint_knowledge_domain_idx ON knowledge (domain);
CREATE INDEX IF NOT EXISTS flint_knowledge_status_idx ON knowledge (status);

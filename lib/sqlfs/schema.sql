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
-- v9(v0.42.0): 계측·파생 산출 쿼리 인덱스 4종 — read_events(ts)·
-- hot_events(ts)·session_starts(ts)·knowledge pending partial
-- (status WHERE status='pending' AND invalid_at IS NULL). 기존 v8 DB는
-- EnsureSchemaV9 게이트가 적용(인덱스 전용, additive — 신규 생성 경로는
-- 게이트 체인이 만든다).
-- v10(v1.2.0): 관측 확장 — read_events에 bytes·zero_reason·source 3열,
-- 항목별 회상 이력 knowledge_reads 테이블(초기 보존 14일). 기존 v9 DB는
-- EnsureSchemaV10 게이트가 적용(additive — ts 인덱스는 게이트가 건다).
-- v11(v1.7.0): decay/강화 스코어 원천 — knowledge에 recall_score(int 기본 0)·
-- last_recalled_at(timestamptz NULL) 2열. 기존 v10 DB는 EnsureSchemaV11
-- 게이트가 적용(additive — 열이 default라 이전 바이너리 읽기 호환).
-- views는 무변경 — 컬럼이 기존 뷰 정의에 영향 없다. v12부터 이 2열은
-- 신규 생성에서 빠진다(아래 v12 참조 — 폐기된 열).
-- v12(v1.9.0): recall append-only 원천 — recall_events 테이블 + 인덱스.
-- 기존 v11 DB는 EnsureSchemaV12 게이트가 적용: score수 합성 이벤트 백필
-- (스코어 연속성 보존용 — 실제 이력 아님) 후 knowledge 2열(v11) 폐기.
-- 신규 DB는 생성 시점부터 2열 없이 v12 종착상태로 직행한다(위 knowledge
-- 정의에 v11 2열이 부재하는 이유). 게이트 체인 재유입 시에도
-- EnsureSchemaV11이 recall_events 존재 판별로 v11 2열 추가를 스킵해
-- add→drop 왕복이 없다(R43 직행 단락 — 게이트 ALTER는 기존 DB 승격 전용).
-- v13(v1.19.0): 관측 기반 계층 기반 — read_events에 near_score(스캔 최고
-- bigramDice, zero 행 전체·near 무관 — near=false 0 마스킹 금지 Ruling),
-- threshold_snapshots(튜닝값 변경 이력 append-only)·weekly_rollups(주간
-- 분자·분모 카운터 — 파생 비율 금지) 테이블. 기존 v12 DB는
-- EnsureSchemaV13 게이트가 적용(additive — DEFAULT 포함 ALTER).
-- v14(v1.20.0): 지식 링크 — knowledge_links 테이블(INSERT 전용·relation
-- 어휘 4종 refines|supports|contradicts|duplicates는 코드 상수로 검증)+
-- (from_id)·(to_id) 인덱스, knowledge (superseded_by) 인덱스(재귀 뷰·
-- 사이클 CTE 탐색 축). 기존 v13 DB는 EnsureSchemaV14 게이트가 적용
-- (additive — FK 미사용, from/to 존재 검사는 쓰기 경로).
-- v17(v1.22.0): 낡음 잔여 — vault_files.content_changed_at(스캔 디프
-- 재점검 큐 스탬프)·knowledge.distilled_at(distill 출력 멱등 스탬프)
-- 2열(NULL 허용)과 knowledge.conditions(수동 재검토 태그, NOT NULL
-- DEFAULT ''). 기존 v16 DB는 EnsureSchemaV17 게이트가 적용(additive —
-- DEFAULT 포함 ALTER, v11/v13 선례).
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
    superseded_by int,                        -- v5: 대체 지식 id(기본 NULL)
    distilled_at  timestamptz,                -- v17: distill 출력 완료 스탬프(기본 NULL — 미출력)
    conditions    text NOT NULL DEFAULT ''    -- v17: 수동 재검토 조건 태그(해시 제외 — hashVolatile)
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
    ids       text NOT NULL DEFAULT '',     -- 반환 지식 id CSV(v0.20.0, 상위 10)
    first_rank integer NOT NULL DEFAULT 0,  -- 첫 topic 정합 순위(v0.30.0, 0=정합 없음/폴백)
    bytes     integer NOT NULL DEFAULT 0,   -- 출력 바이트(v1.2.0 G5 — 표면별 cost 산출)
    zero_reason text NOT NULL DEFAULT '',   -- 'unowned'|'owned_miss'|''(v1.2.0 — zero=1 행만)
    source    text NOT NULL DEFAULT '',     -- 'hook'|'cli'|'mcp'(v1.2.0 — 채널 믹스)
    near_score double precision NOT NULL DEFAULT 0 -- 스캔 최고 bigramDice(v1.19.0 — zero 행 전체, near 무관·쿼리 본문 비저장)
);

-- v10: 항목별 회상 이력 1행(hot 헤드라인 포함 — G4 계상 원천).
-- 지식 id·시각·세션 라벨·표면만 남긴다(개인 문구 부재 원칙).
-- 보존 초기 14일(read_events와 동일 창 — PruneReadEvents 정리).
CREATE TABLE IF NOT EXISTS knowledge_reads (
    id           serial PRIMARY KEY,
    knowledge_id integer NOT NULL,
    ts           timestamptz NOT NULL DEFAULT now(),
    session      text NOT NULL DEFAULT '',  -- 호출자 세션 라벨(선택)
    kind         text NOT NULL DEFAULT 'read' -- 'hot'|'read'(주입 표면)
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

-- v12: 회상 이벤트 1행(append-only — 다중 기기 동기화 시 충돌 없는
-- 회상 원천. 감쇠 유효점수는 이벤트 파생). 개인 문구 부재 원칙 승계.
CREATE TABLE IF NOT EXISTS recall_events (
    knowledge_id integer NOT NULL,
    recalled_at  timestamptz NOT NULL DEFAULT now()
);

-- v12: 파생 랭킹 조인(count·max(recalled_at)) 경로 인덱스.
CREATE INDEX IF NOT EXISTS idx_recall_events_id_time
    ON recall_events (knowledge_id, recalled_at DESC);

-- v13: 튜닝값 변경 이력 1행(append-only — 변경 행만 기록, 동기(fired) 캡처는
-- B3 등록표 시점 이월). 개인 문구 부재 원칙 승계(키·값만).
CREATE TABLE IF NOT EXISTS threshold_snapshots (
    ts        timestamptz NOT NULL DEFAULT now(),
    key       text NOT NULL,               -- thresholds 메타 키
    old_value text NOT NULL,               -- 변경 전 값
    new_value text NOT NULL                -- 변경 후 값
);

-- v13: 키별 최근 스냅숏 조회 경로 인덱스.
CREATE INDEX IF NOT EXISTS idx_threshold_snapshots_key_time
    ON threshold_snapshots (key, ts DESC);

-- v13: 주간 롤업 분자·분모 카운터(파생 비율 금지 — 소비 시점 계산.
-- metric: zero_pct·fallback_pct·pair_pct, 정의는 METRICS.md가 단일 소스).
CREATE TABLE IF NOT EXISTS weekly_rollups (
    week_start date   NOT NULL,            -- ISO 주 시작(UTC 월요일)
    metric     text   NOT NULL,
    num        bigint NOT NULL,            -- 분자 카운터
    den        bigint NOT NULL,            -- 분모 카운터
    PRIMARY KEY (week_start, metric)
);

-- v3(ADR-0008): 검색 필터 경로 인덱스 — 신규 DB 생성 시점에 포함.
CREATE INDEX IF NOT EXISTS flint_knowledge_entity_idx ON knowledge (entity);
CREATE INDEX IF NOT EXISTS flint_knowledge_domain_idx ON knowledge (domain);
CREATE INDEX IF NOT EXISTS flint_knowledge_status_idx ON knowledge (status);

-- v14(v1.20.0): 지식 링크 1행(INSERT 전용 — 갱신·삭제 경로 부재).
-- relation 어휘는 코드 상수(refines|supports|contradicts|duplicates)로
-- 쓰기 경로에서 검증(FK 미사용 — from/to 존재 검사도 쓰기 경로).
CREATE TABLE IF NOT EXISTS knowledge_links (
    id         bigserial PRIMARY KEY,
    from_id    bigint NOT NULL,
    to_id      bigint NOT NULL,
    relation   text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- v14: 링크 양방향 탐색 경로 인덱스.
CREATE INDEX IF NOT EXISTS idx_knowledge_links_from_id ON knowledge_links (from_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_links_to_id ON knowledge_links (to_id);

-- v15(v1.20.1): 링크 유일성 봉인 — (from_id, to_id, relation) 유니크
-- 인덱스. v1.20.0의 NOT EXISTS 가드는 검사-삽입 창을 남겨 병렬 적립에서
-- 같은 링크가 2행씩 적립될 수 있었다. INSERT는 ON CONFLICT DO NOTHING.
CREATE UNIQUE INDEX IF NOT EXISTS uq_knowledge_links_triple
    ON knowledge_links (from_id, to_id, relation);

-- v14: supersede 체인(재귀 뷰·사이클 CTE) 탐색 축 인덱스.
CREATE INDEX IF NOT EXISTS idx_knowledge_superseded_by ON knowledge (superseded_by);

-- v16 (v1.21.0): vault bridge pointer snapshot — one vault_files row per
-- vault .md file. The original text is never copied, path UNIQUE is the
-- scan-diff UPSERT axis, and content_hash = sha256 of the raw bytes —
-- deliberately distinct from the knowledge normalized hash. Existing v15
-- DBs are promoted by the EnsureSchemaV16 gate (additive). An audit
-- staging target (auditStageTables).
CREATE TABLE IF NOT EXISTS vault_files (
    id              bigserial PRIMARY KEY,
    path            text NOT NULL UNIQUE,
    title           text NOT NULL,
    tags            text NOT NULL DEFAULT '',
    mtime           timestamptz NOT NULL,
    size            bigint NOT NULL,
    content_hash    text NOT NULL,
    summary         text NOT NULL DEFAULT '',
    last_indexed_at timestamptz NOT NULL DEFAULT now(),
    content_changed_at timestamptz           -- v17: 스캔 디프 변경 스탬프(기본 NULL — 재점검 큐)
);

-- v16: duplicate-note detection axis (identical content under different paths).
CREATE INDEX IF NOT EXISTS idx_vault_files_content_hash ON vault_files (content_hash);

-- flint 뷰 (spec §6, §7)
-- active_knowledge: 무효 사실(invalid_at) 차단 — read는 이 뷰로 강제.
-- v4(spec §2): 참조 계측 컬럼(read_count·last_read_at)도 노출 — RankOrder의
-- last_read_at 정렬(T68)이 이 뷰를 대상으로 하므로 누락 시 42703.
-- 기존 컬럼·순서 불변, 신규 컬럼은 말미에 추가(views_sig 안정).
CREATE OR REPLACE VIEW active_knowledge AS
SELECT id, topic, summary, tags, entity, domain, confidence, origin,
       status, content_hash, session_id, valid_at, ingested_at,
       read_count, last_read_at
FROM knowledge
WHERE invalid_at IS NULL;

-- hot_view: 주입용 — 활성 프로젝트 + pending 지식 + 최근 결정 7일.
-- v0.41.0 T5(단일 정의): 말미에 valid_at·ingested_at 추가 노출(프로젝트
-- 팔은 NULL) — 증분 id·pending 대기일수 접미를 이 뷰 조회 하나로 소비하고,
-- 세 팔 조건을 다시 모은 동반 질의(hot.go)는 폐지됐다. v0.6 룰링(CI
-- 39713526850)의 ORDER BY ts DESC, id DESC tie-break은 유지 — 동률 ts의
-- 완전 순서 재료. 기존 컬럼·순서 불변, 신규 컬럼은 말미 추가 —
-- views_hash 불일치로 이중 게이트가 재적용한다.
CREATE OR REPLACE VIEW hot_view AS
SELECT 'project' AS kind, p.name AS topic, p.status AS summary, p.updated AS ts,
       NULL::int AS id, NULL::timestamptz AS valid_at, NULL::timestamptz AS ingested_at
FROM projects p
WHERE p.status = 'active'
UNION ALL
SELECT 'pending' AS kind, k.topic, k.summary, k.ingested_at, k.id, k.valid_at, k.ingested_at
FROM knowledge k
WHERE k.invalid_at IS NULL AND k.status = 'pending'
UNION ALL
-- v0.24.2(개인화 평가 실측): 결정 판정을 tag='adr' 기준으로 —
-- 실사용 관측에서 decision domain 관례는 채택되지 않았다(결정은
-- domain=main+tag=adr 관례로 기록된다). 기획 리뷰 대안 (b) 채택.
SELECT 'decision' AS kind, k.topic, k.summary, k.valid_at, k.id, k.valid_at, k.ingested_at
FROM knowledge k
WHERE k.invalid_at IS NULL
  AND k.ingested_at >= now() - interval '7 days'
  AND k.tags LIKE '%:adr:%';

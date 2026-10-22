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
CREATE OR REPLACE VIEW hot_view AS
SELECT 'project' AS kind, p.name AS topic, p.status AS summary, p.updated AS ts
FROM projects p
WHERE p.status = 'active'
UNION ALL
SELECT 'pending' AS kind, k.topic, k.summary, k.ingested_at
FROM knowledge k
WHERE k.invalid_at IS NULL AND k.status = 'pending'
UNION ALL
SELECT 'decision' AS kind, k.topic, k.summary, k.valid_at
FROM knowledge k
WHERE k.invalid_at IS NULL
  AND k.ingested_at >= now() - interval '7 days'
  AND k.domain = 'decision';

# flint v1.25.0 스파이크 — audit_log 리플레이 설계·실증 (2026-11-23)

상태: active
원천: docs/plans/2026-11-23-flint-v1250.md T3 ② — v2.0.0 스코프
(dolt 시스템 테이블 감사 의존 대체/병행 검토의 기술 실증)
준수: CLAUDE.md(합성 데이터) — 실증은 격리 scratch DB에서만(종료 DROP)

## 0. 질문

앱 계층 이벤트 로그(audit_log·audit_event)만으로 dolt 시스템
테이블(dolt_log·dolt_commit_diff_knowledge)의 감사 쿼리 의미론을
순수 PostgreSQL로 재현할 수 있는가.

## 1. (a) 스키마 설계안 DDL

dolt_log 대응 — seq 단조·tx_id·message·ts:

```sql
CREATE TABLE audit_log (
    seq     bigserial PRIMARY KEY,   -- commit_order 대응(단조)
    tx_id   text NOT NULL UNIQUE,    -- commit_hash 대응
    message text NOT NULL,           -- 커밋 메시지
    ts      timestamptz NOT NULL DEFAULT now()
);
```

dolt_commit_diff_knowledge 대응 — 테이블별 이벤트 행
(diff_type·to_id/from_id·topic):

```sql
CREATE TABLE audit_event (
    vid        bigserial PRIMARY KEY,          -- 이벤트 단조 id(latest-wins 기준)
    log_seq    bigint NOT NULL REFERENCES audit_log(seq),
    table_name text NOT NULL,                  -- 'knowledge' | ...
    diff_type  text NOT NULL CHECK (diff_type IN ('added','modified','removed')),
    to_id      integer,
    from_id    integer,
    to_topic   text,
    from_topic text
);
CREATE INDEX audit_event_log_seq_idx ON audit_event(log_seq);
CREATE INDEX audit_event_row_idx ON audit_event(table_name, COALESCE(to_id, from_id));
```

### 대체 대상 3쿼리 (실제 SQL — lib/db)

1. `lib/db/diff_store.go:24-33` diffCommitsSQL(DiffCommits 원천) —
   dolt_log×dolt_diff 범위 요약:

```sql
SELECT l.commit_hash, l.date, l.message,
  COALESCE(bool_or(d.data_change), false),
  COALESCE(bool_or(d.schema_change), false)
FROM dolt_log l
LEFT JOIN dolt_diff d ON d.commit_hash = l.commit_hash
WHERE l.commit_order > (SELECT commit_order FROM dolt_log WHERE commit_hash = $1)
  AND l.commit_order <= (SELECT commit_order FROM dolt_log WHERE commit_hash = $2)
GROUP BY l.commit_hash, l.commit_order, l.date, l.message
ORDER BY l.commit_order DESC
LIMIT 20
```

2. `lib/db/diff_store.go:41-46` DiffRows — 두 커밋 간 knowledge 행 diff:

```sql
SELECT diff_type, COALESCE(to_id, from_id), COALESCE(to_topic, from_topic)
FROM dolt_commit_diff_knowledge WHERE to_commit = $1 AND from_commit = $2
```

3. `lib/db/history.go:31` LogEntries(logLatestN 조립 — :119-137) —
   최근 N 커밋 목록(필터·LIMIT $1):

```sql
SELECT commit_hash, committer, date, message FROM dolt_log
[WHERE message NOT LIKE $n [AND message LIKE $m]]
ORDER BY commit_order DESC LIMIT $1
```

### 계약 경계 — wire.Store history 5메서드 (lib/wire/store.go:17-23)

`LogEntries`·`QueryAsOf`·`DiffRows`·`DiffStat`·`DiffCommits`는
인터페이스 계약 — 저장 구현(dolt 시스템 테이블 ↔ audit_log)을
어느 쪽으로 바꿔도 이 시그니처와 호출자(lib/cli)는 불변이다.
대체 시 `Repo` 구현 내부만 교체된다. `QueryAsOf`(시점 복원)는
이번 3쿼리 대상 밖 — audit_log 상태 재구성으로 확장 가능하나
별도 실증 필요(§4 유의점).

## 2. (b) diff 재현 실증 — 순수 PG 쿼리·실행 결과

환경: 합성 scratch 전용 DB. postgres:16-alpine 컨테이너 기동을 먼저
시도했으나 docker VM 디스크 포화로 initdb 실패 — 과제가 허용한
대안("기존 PG")인 로컬 PostgreSQL 15.17의 전용 DB
`flint_t3_auditlog_spike`에서 실행, 종료 시 DROP. 사용 기능
(window 함수·FULL JOIN·`IS DISTINCT FROM`)은 전부 PG 9.x 이래
표준이라 버전 차이 영향 없음. 데이터는 합성 5커밋(tx0001~tx0005:
seed 3행·수정·은퇴+재수정·덧셈 후 제거).

**Q1 — LogEntries 대응** (message NOT LIKE + 최근 N):

```sql
SELECT tx_id, message, ts FROM audit_log
WHERE message NOT LIKE 'telemetry%'
ORDER BY seq DESC LIMIT 3;
```

결과: tx0003→tx0001 3행, DESC 순서·필터 정상.

**Q2 — DiffCommits 대응** (범위 요약, from seq 1 배제·to seq 3 포함):

```sql
SELECT l.tx_id, l.ts, l.message, COALESCE(bool_or(e.vid IS NOT NULL), false) AS data_change
FROM audit_log l LEFT JOIN audit_event e ON e.log_seq = l.seq
WHERE l.seq > 1 AND l.seq <= 3
GROUP BY l.seq, l.tx_id, l.ts, l.message
ORDER BY l.seq DESC LIMIT 20;
```

결과: tx0003(t)·tx0002(t) — dolt_log×dolt_diff 요약과 동형.

**Q3 — DiffRows 대응** (두 seq 시점 knowledge diff, latest-wins):

```sql
WITH ranked AS (
  SELECT e.diff_type, e.to_id, e.from_id, e.to_topic, e.from_topic,
         row_number() OVER (PARTITION BY COALESCE(e.to_id, e.from_id)
                            ORDER BY e.vid DESC) AS rn
  FROM audit_event e
  WHERE e.table_name = 'knowledge' AND e.log_seq > 1 AND e.log_seq <= 3
)
SELECT diff_type, COALESCE(to_id, from_id) AS id, COALESCE(to_topic, from_topic) AS topic
FROM ranked WHERE rn = 1 ORDER BY id;
```

결과 — `dolt_commit_diff_knowledge` 출력 열(diff_type·id·topic)과
동일 조립:

```text
 diff_type | id |       topic
-----------+----+--------------------
 removed   |  1 | spike-demo-alpha
 modified  |  2 | spike-demo-beta-v3
 added     |  4 | spike-demo-delta
```

id2가 seq2(v2)가 아니라 seq3(v3)로 나오는 latest-wins 판정도 정상.

### 경계 사례 — 범위 내 added→removed(순변화 0)

latest-wins 쿼리는 seq 3→5 범위(id5 추가 후 제거)에서
`removed 5`를 오탐한다 — dolt_commit_diff(엔드포인트 상태 비교)는
0행이 정답. 엔드포인트 상태 재구성 쿼리로 dolt 의미론 정합을
확인했다(각 시점의 최신 이벤트가 added/modified인 행 = 존재):

```sql
WITH ev AS (
  SELECT COALESCE(to_id, from_id) AS id, diff_type, to_topic, log_seq, vid
  FROM audit_event WHERE table_name = 'knowledge' AND log_seq <= 5
),
s_from AS (
  SELECT id, to_topic FROM (
    SELECT id, diff_type, to_topic,
           row_number() OVER (PARTITION BY id ORDER BY vid DESC) rn
    FROM ev WHERE log_seq <= 3) x
  WHERE rn = 1 AND diff_type <> 'removed'
),
s_to AS (
  SELECT id, to_topic FROM (
    SELECT id, diff_type, to_topic,
           row_number() OVER (PARTITION BY id ORDER BY vid DESC) rn
    FROM ev) x
  WHERE rn = 1 AND diff_type <> 'removed'
)
SELECT CASE WHEN f.id IS NULL THEN 'added' WHEN t.id IS NULL THEN 'removed' ELSE 'modified' END AS diff_type,
       COALESCE(t.id, f.id) AS id, COALESCE(t.to_topic, f.to_topic) AS topic
FROM s_from f FULL JOIN s_to t ON t.id = f.id
WHERE f.id IS NULL OR t.id IS NULL OR f.to_topic IS DISTINCT FROM t.to_topic
ORDER BY id;
```

결과: seq 3→5 = **0행**(정합 — 순변화 없음), seq 1→3 = Q3와 동일
3행. 즉 오탐 회피 형태까지 순수 PG로 재현 가능하다.

## 3. (c) 결론 — 성공 (대체 기술 가능성 입증)

**순수 PostgreSQL만으로 dolt_log·dolt_commit_diff_knowledge의
감사 쿼리 의미론(3쿼리) 재현에 성공.** 단 이 실증은 "대체 가능함"의
기술 입증일 뿐, 대체 vs 병행 채택은 v2.0.0 G2 Ruling 과제다.
Ruling이 병행을 고려할 때의 검토 논점(이 스파이크에서 식별):

- **감사 순수성 이동** — dolt 커밋의 불변 블롭 보증이 사라지고
  감사의 무결성이 앱 계층 이벤트 기록(트리거 vs 이중 쓰기)에
  의존하게 된다 — 기록 누락 시 감사 공백(실패 시 병행 우세 근거).
- **latest-wins 오탐** — 채택 시 엔드포인트 재구성 쿼리 사용 또는
  오탐 수용 판정이 선행되어야 한다(§2 경계 사례).
- **QueryAsOf 확장 실증 별도 필요** — 시점 복원 상태 재구성 비용.
- 이벤트 테이블 증가량 관리(GC·보존 정책)가 새 운영 부담이 된다.

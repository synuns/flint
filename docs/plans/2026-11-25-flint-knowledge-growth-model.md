# 지식 성장 모델 — 근본 원칙 재정립·work_state·수집 3단 (2026-11-25)

상태: active

- **진행**: 기획 확정(사용자 승인 2026-11-25)·1단계 빌드 중 — v1.38.0 플랜(docs/plans/2026-11-26-flint-v1.38.0.md) 디스패치 완료
- **선행 문서**: docs/plans/2026-11-19-flint-knowledge-pipelines.md(§0 철학 P1·P2·K1~K5 상속)·
  docs/plans/2026-11-21-flint-release-scope.md(릴리스 편성 단일 소스)
- **감사**: 독립 2각도+자체 검증 21건 — 수용 16·부분 2·기각 3(§9)
- **관계**: knowledge-pipelines 로드맵의 교정·낡음·환류 계열을 **대체하지 않고 상속**한다.
  본 문서의 신규분은 ①존재론 분리(work_state) ②claim 축 ③수집 3단 정식화다.

## 0. 목적

사용자 목표 — **에이전트의 지속적인 성장과 지식관리**. 모델은 동결되어 있으므로
성장은 전부 외재화된 상태에서 나온다. flint는 기억의 저장소가 아니라
**경험→지식→행동→검증의 대사 회로**다. 본 문서는 이 회로의 근본 원칙을
재정립하고, 그것에서 도출되는 세 가지 구조 변경을 확정한다.

## 1. 철학 — 상속과 신설

기존 K원칙을 그대로 상속한다(P1 오라클 없음·P2 기억의 주인은 사용자·
K1 현상태 가설·K2 증거 가중치·K3 저장은 비용 회상이 가치·K4 수정은 무효화·
K5 사실은 조건·출처·시점과 한 몸). 신설 공리 4개:

- **G1 신념/의도 존재론 분리** — 신념(세상이 어떤가)은 참/거짓으로 실패하고,
  의도(앞으로 무엇을 한다)는 이행/파기로 실패한다. 진리 만족자가 다르므로
  knowledge와 work_state는 다른 테이블·다른 수명·다른 연산
  (supersede vs resolve)을 갖는다. 저장소 결합이 아니라 상위 합성으로 만난다.
- **G2 이벤트 기록은 지식이 아니라 정당화의 증거다** — "릴리스 완료" 같은
  발생 기록은 영원히 참이지만 행동을 바꾸지 못한다. 지식의 근거로 참조되고,
  회상이 아니라 명시 조회로 섬긴다.
- **G3 성장은 축적이 아니라 대사다** — K3의 확장. 건강도는 저장량이 아니라
  획득→검증→적용→확인/퇴역 회전의 속도·정확도다. 회상되지 않는 행은
  대사 찌꺼기다(이미 decay·dead_pct로 부분 실현 — 회로 전체로 정식화만).
- **G4 가치는 사후 증명된다** — P1의 귀결. 수집 시점의 유용성 판정은
  문맥에 묶인 예측일 뿐이고, 예측이 틀린 비용은 전부 손실로 귀결된다.
  그래서 폐기 대신 **시험 기간(pending·만료)**으로 보류하고, 참여 카운터가
  증명하면 승격한다.

**claim 축(신설)** — 유형 분류 기준은 "무엇을 주장하는가"(진리 만족자)다:
`decision`(권한 — 정해졌다)·`procedure`(효능 — 통한다)·`lesson`(인과 —
이래서 망했다)·`event`(발생 — 일어났다). 진리 만족자가 다르면 검증 방법과
부패 방식이 다르므로 수명 규칙이 달라진다. 기존 `kind`(기록 유형:
knowledge/session/project)와 직교하는 별도 열이며, 변동성 3계층
(pipelines §5 — 불변/조건부/휘발)과도 직교다(claim=무엇을 주장하나,
변동성=얼마나 변하나). 5유형에서 fact를 삭제한 것은 fact가 실재로
decision/procedure에 흡수되기 때문이다.

## 2. 갭 (2026-11-25 실측)

1. **작업 상태 공백** — `sessions.tasks`는 done 경로에서 `"[]"` 하드코어.
   진행 과업·다음 액션이 flint에 없으니 세션 간 승계가 매번 재발견으로 시작한다.
2. **증류 저품질** — compact 원문이 그대로 pending 적립된 행 관측
   (topic이 원문 절단). "요약"이 아니라 "원문 절단" 상태.
3. **event성 기록이 회상 후보** — 릴리스 이력류가 active 지식과 같은
   회상 경로를 공유해 2,000자 예산을 오염시킨다(G2 위반 상태).
4. **claim 축 부재** — 진리 만족자별 수명·검증 규칙을 표현할 축이 없다.

## 3. 수집 3단 파이프라인

| 단 | 규칙 | 근거 |
|---|---|---|
| 수집 | 후보는 폐기하지 않고 **시험 기간 보류**: pending(만료 가능·dedup·상한). **원문 복사 금지 — 검증자가 거부(exit 3)** | G4·P1 |
| 승격 | **Δ≠0 승격 심사**: "이 세션이 다음 세션의 행동을 바꿀 것이 하나라도 있는가"를 판정 근거로 요구 — 0건 정상. 심사 통과분만 `flint approve`로 active | G1·K2 |
| 검증 | 참여 카운터(read_count·recall_events·재조회 신호)가 사후 증명/반증 — 순위 반영은 2단계(환류 계열과 병합 재판정) | G3·K3 |

- **work_state는 게이트 예외** — 세션 종료 시 **무조건 기록**한다(같은
  state_hash면 기계 스킵). 게이트를 상태에도 적용하면 Δ=0 세션에서
  승계가 끊겨 본 설계가 고치려는 증상이 재발한다(감사 B1).
  기록 경로는 병용이다: 세션 중 에이전트 명시 기록(`flint state` —
  SKILL 계약, v1.36.0 B안 계승) + done 훅 payload의 tasks 필드 확장
  (기존 훅 확장은 v1.36.0 결정상 허용 — 하네스 제공분). 무기록 세션은
  기존 `stale_sessions` 카운터로 관측한다. done은 수신한 tasks를
  `sessions.tasks`에 채운다("[]" 하드코어 폐지).
- **세션 원문 자동 적립 폐지** — compact/done의 maybeSessionKnowledge
  원문 적립을 제거한다(세션 행 기록은 유지). `session_knowledge_days`는
  deprecated(빌드 시 TUNING §2에 표기). 기존 저품질 pending 행은
  `maintain --expire-pending` 자연 소멸+수동 검수로 정리.
- **기계 신호 범위(1단계)** — 현존 신호만: read/recall 카운터·open 잔존·
  세션 행 존재. PostToolUse 신규 훅은 v1.36.0 교정 입구 결정
  (신규 훅 신설 금지·기존 훅 확장만)과 충돌하므로 **기각** —
  필요 시 기존 훅 확장 한정으로 재판정한다.

## 4. work_state 설계

```sql
CREATE TABLE IF NOT EXISTS work_state (
    id           serial PRIMARY KEY,
    project_id   integer REFERENCES projects(id),   -- nullable: 미소속 세션 허용
    agent        text NOT NULL DEFAULT 'claude',
    status       text NOT NULL DEFAULT 'open',      -- open | resolved
    task         text NOT NULL,                     -- 진행 과업
    detail       text NOT NULL DEFAULT '',          -- 시도·실패 맥락
    next_action  text NOT NULL DEFAULT '',
    state_hash   text NOT NULL,                     -- 서버측 정규화 해시
    session_id   integer REFERENCES sessions(id),
    opened_at    timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    resolved_at  timestamptz,
    resolved_by  integer                            -- 소프트 레퍼런스(FK 미선언):
                                                    -- retention·supersede와 독립,
                                                    -- 참조 대상 무효화 시 표기만
);
CREATE UNIQUE INDEX IF NOT EXISTS flint_work_state_open_idx
    ON work_state (project_id, state_hash) WHERE status = 'open';
```

- **state_hash는 서버가 계산한다** — task 문자열을 NFKC·trim·소문자·기호
  접기 정규화 후 해시. 에이전트 서술이 매번 달라져도 개방 중 재보고는
  같은 행 갱신으로 흡수된다(감사 A2). resolve 후 재개는 새 행 — 과업 이력 보존.
- **수명** — `status='open'`이 `state_stale_days`(신설 기본값 14일) 동안
  무접촉이면 **hot 스냅샷에서 자동 제외**(주입 최소 원칙)하고,
  resolve 제안은 `maintain --stale-state` **승인제**(decay-audit 패턴 —
  K4 자동 삭제 없음 준수). resolved 행은 retention 대상 아님(무기한
  보존)·세션 시작 주입에서 제외(요청 시 `flint state list --all` 조회).
- **접점** — CLI `flint state open|list|resolve`·MCP `flint_work`
  (open/update/resolve/list). `doctor --check secrets` 스캔 대상에
  task·detail·next_action 3열을 추가한다(유출 표면 = knowledge 이상).
- **hot 합성** — 주입은 "지식 브리프 + open_work_state 스냅샷"을 상위에서
  합성한다. 스냅샷은 최대 `state_snapshot_max`건(신설 기본값 3)·
  선정은 현재 프로젝트 일치 우선 → updated_at 최신순(빌더 콜 —
  무관 과업 주입 방지)·hot_budget 내 하위 배분 상한을 갖는다
  (지식 예산 보호 — 감사 A11). 1단계 한계: sessions에 프로젝트
  연결 열이 없어 hot의 현재 프로젝트 식별 통로가 없다 — 1단계는
  최신순 단일 규칙으로 작동하고 프로젝트 우선 분기는 연결 스키마와
  함께 2단계 재판정(v1.38.0 빌드 룰링 2026-11-26).

## 5. 스키마·뷰·읽기 경로

```sql
ALTER TABLE knowledge ADD COLUMN IF NOT EXISTS claim text NOT NULL DEFAULT '';
-- backfill(같은 마이그레이션): UPDATE knowledge SET claim='event' WHERE claim='';
--   미분류 기존 행은 G2 기준 event로 정직하게 분류하고, 명백한 decision/
--   procedure/lesson만 채택 세션에서 수동 선별 재분류한다(벌크 스크립트·
--   lazy 자동 재분류는 기각 — §9).
CREATE INDEX IF NOT EXISTS flint_knowledge_claim_idx ON knowledge (claim);

CREATE OR REPLACE VIEW recall_knowledge AS
SELECT id, topic, summary, tags, entity, domain, claim, confidence, conditions, status
FROM knowledge
WHERE invalid_at IS NULL AND claim <> 'event';

CREATE OR REPLACE VIEW open_work_state AS
SELECT id, project_id, task, detail, next_action, updated_at
FROM work_state WHERE status = 'open';
```

구현 노트(2026-11-26 v1.38.0 감사 반영): 두 뷰는 lib/sqlfs/views.sql이 아니라
lib/db/admin.go 스키마 게이트 내 인라인 정의다(views_sig 감시망 밖 — 문서·코드
동일 게이트는 doclint와 감사 루프가 담당). recall_knowledge는 status 열을
포함한 10열(§5 초안 9열에서 status 추가 — read --fields id,status 계약).
resolved 행 보존 판정 갱신(2026-11-27 v1.38.3·R143 각도3): 무기한 보존에서
**보존 기한 정리로 뒤집힘** — `work_state_resolved_retention_days`(기본 90)·
`maintain --stale-state --commit`이 resolved 행을 DELETE(R143-3호 1: resolved
누적이 secrets_scan_row_limit를 고갈시키는 결함 수리).

- **scope 신설 없음** — 유효 범위 앵커는 기존 `conditions`(조건 태그,
  K5 실현)를 재사용한다. 회상 시점 조건 평가·자동 탈락은 P1 위반이므로
  하지 않는다 — 기존 회상 건강 플래그(provenance suffix) 경로로 표시한다.
- **read 기본 경로** — recall_knowledge 뷰 경유(event 제외).
  event는 `--claim event` 명시 조회. `read --claim <값>` 필터 신설.
- **마이그레이션 직후 회상 공백 방지** — backfill+수동 선별 재분류를
  같은 릴리스 내 채택 세션에서 수행한다(감사 A13).

## 6. 품질 검증자 (계약 유도, v1.36.0 B안 계승)

"같은 스킬 규칙이 원문 절단을 냈는데 왜 이번엔 다른가"에 대한 답은
**코드가 계약을 강제하는 것**이다. claim 게이트는 write가 아니라
**approve 단일 지점**에 둔다(빌더 체크 반영 — import 재수입 호환·
직행 active 우회 모두 하나의 목으로 봉쇄):

1. **claim 없는 knowledge write는 confidence와 무관하게 pending 강제**
   (시험 기간 — G4). write는 거부하지 않는다.
2. **approve는 claim 확정의 단일 게이트** — claim 미지정(또는 backfill
   'event') 행의 승격 시도 → exit 3, `--claim` 재지정 유도.
   CLI·MCP flint_approve 동일. import(항상 pending)도 같은 규칙을 통과.
3. knowledge payload의 summary가 해당 세션 요약 원문과 동일(또는 접두 일치)
   → exit 3 — 세션 요약 복사의 기계적 거부(감사 B4).
4. work_state task의 서버측 정규화·해시(§4) — 클라이언트 정규화 비의존.
5. conditions·confidence 등 기존 검증 재사용(신규 경로 아님).

## 7. 수명 규칙 총표

| 대상 | 생성 | 갱신 | 종료 |
|---|---|---|---|
| knowledge(decision) | write(claim 필수) | supersede로만 교체 | invalid_at |
| knowledge(procedure/lesson) | 동일 | 카운터(read/recall) — 강화 공식 없음 | decay·감쇠 감사 |
| knowledge(event) | 동일 | 없음 | 무기한 보존(회상 제외) |
| pending 후보 | write(심사 전) | 없음 | expire-pending |
| work_state(open) | **무조건 기록** | state_hash 흡수 갱신 | resolve(승인)·stale 주입 제외 |
| work_state(resolved) | — | 없음 | 주입 제외·보존 기한(90일) 후 stale-state 정리(v1.38.3 갱신) |

## 8. 단계 편성

- **1단계(단일 마이너)** — §5 스키마+뷰·§4 work_state(DDL·CLI·MCP·
  secrets·hot 합성)·§3 검증자·maybeSessionKnowledge 폐지·
  `read --claim`·SKILL/TUNING/contracts 동반 갱신. 문서=코드 동일 게이트.
- **2단계(후행·별도 재판정)** — 참여 카운터의 순위 반영(pipelines §12
  환류·자율 튜닝 계열과 병합). PostToolUse 신규 훅은 기각(§3).
- **비목표** — 벌크 세션 아카이브·신뢰 강화/약화 공식(원시 카운터로 충분)·
  recall 시점 dedup 이동(content_hash dedup 유지)·회상 시점 조건 자동 평가(P1).

## 9. 감사 판정 기록 (2026-11-25)

독립 감사 2각도(기술 정합성 14건·요구 충실도 5건)+자체 검증 7건 —
중복 정리 21건: **수용 16·부분 수용 2·기각 3**.

- 치명 수리 4건(모두 본문 반영): Δ≠0 게이트 knowledge 한정(§3)·
  유형 5→3+event 축소(§1)·품질 검증자(§6)·state_hash 서버측 정규화(§4).
- 수용 주요: stale open 수명(§4)·conditions 재사용+플래그 격하(§5)·
  기계 신호 현존 한정(§3)·사후 검증 루프 단계 분리(§3·§8)·
  backfill+수동 선별(§5)·MCP flint_work·secrets 스캔·read 기본 뷰·
  hot 배분(§4)·project_id/resolved_by nullable·
  LSP 인용 교정(근거를 "계약 차이" 일반 서술로 — G1)·
  resolved 주입 제외·역치는 thresholds.go+meta 오버라이드만.
- 기각 3건(사유): dedup recall 시점 이동(write dedup이 저비용 검증 경로)·
  lazy 자동 재분류(회상 경로 인지 부담)·강화 계약 변경(claim 축소로 전제 소멸).
- 사용자 반례(2026-11-25): "행동을 바꾼 것만 지식이면 다른 문맥에서
  바꾸지 못할 수 있다" → G4 공리로 정식화: 게이트는 폐기 기준이 아니라
  **승격 심사**이고, 미증명 후보는 시험 기간 보류로 잃지 않는다(§3).
- 빌더 체크(2026-11-25, 신규 관점 5질문 — 사용자 몫 0건·전부 기획 범위 내
  해소): state 기록 경로=에이전트 명시+done 훅 확장 병용(§3)·
  **claim 게이트를 approve 단일 지점으로**(구 write exit 3 대체 — §6)·
  스냅샷 선정=프로젝트 일치 우선→최신순(§4 빌더 콜)·import는 승격 규칙
  경유로 호환 유지(§6)·resolved_by 소프트 레퍼런스(§4).

## 10. 문서 동반 갱신

- **이번 기획 커밋**: 본 문서·PLAN.md(§3 철학·§6 마일스톤)·
  README.md/README.ko.md(How It Works 철학 단락 — 현재 구현분만)·
  flint-plugin/skills/flint/SKILL.md(3규칙 중 쓰기 규칙을 Δ≠0·
  pending 보류·원문 금지로 — 현재 구현분만)+설치본 동기화.
- **1단계 빌드 동반**: TUNING §2(`state_stale_days`·`state_snapshot_max`
  신설·`session_knowledge_days` deprecated)·contracts/cli.md
  (`flint state`·read --claim)·SKILL work_state 절·README 커맨드 표.

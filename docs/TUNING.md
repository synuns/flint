# flint 임계값 튜닝 가이드

flint의 임계값·예산은 하드코딩 금지 원칙 아래 meta 테이블로 오버라이드한다.
기본값은 코드(`lib/store/thresholds.go`)에 있고, meta 키가 우선한다.
잘못된 값은 경고와 함께 기본값으로 폴백한다(에러 아님).

## 0. 조정 가이드 (v0.24.0 — 임계값 설계 점검 반영)

**언제 조정하나** — 기본값으로 시작하고 **관측 창(14일) 후 첫 반복
경고 패턴에서만** 오버라이드한다. 관측 없는 선제 튜닝은 분포를 모른
채 값을 고르는 것(관측 선행 원칙 — zero-near의 표본 하한이 이 원칙의
기존 구현).

**재조정 트리거** — 아래 패턴이 반복될 때만 해당 키를 조정한다:

| 반복 관측 | 조정 키 | 방향 |
|---|---|---|
| 어휘 밖 경고가 계속(오타 아님) | vocab_tags·vocab_domains | 개인 어휘 추가 |
| zero-near warn 반복 | suggest_min_score | 하향(제안 문턱) |
| zero-near '판정 보류' 지속 | — | 조정 아님 — 표본 축적 대기 |
| pending-age warn 반복·방치 | pending_max 또는 승격 플로 | 값보다 프로세스 |
| dup_susp warn 반복 | dup_susp_max | 상향(정상 다양성 수용) |
| 회상 부재 누적(doctor recall-starved) | recall_warn_writes | 하향(경고 문턱) |
| 폴백률 지속 상승 | (검색 품질 영역 — PLAN §15) | 키 아님 |

**경고는 행동 가능해야 한다** — 조치를 못 정하면 튜닝 대상이 아니다.
매 경고는 fix 안내를 동반한다(doctor). 발동 창구: 대부분 수동
doctor·maintain이며 세션 시작 안내는 pending·갱신만(비간섭 §13).

## 1. hot 예산(주입 상한) 튜닝

절차(§15 이연결정 "주입 예산 상한: hot_view 2주 실측 p95로 확정" 회수):

1. **관측(2주)** — 세션마다 `flint status`의 `hot_calls`·`hot_p95`를 기록한다
   (v0.3 계측 — 프로세스 내 통계, 관측 세션 단위).
2. **예산 갱신** — 현재 hot 상한은 `hot_budget` 메타 키로 확정한다(v0.4부터
   `flint hot`이 매 실행 이 키를 읽는다 — 미설정·무효 값은 기본 2,000):
   ```sh
   psql "$FLINT_DSN" -c "INSERT INTO meta (key, value) VALUES ('hot_budget','2400')
     ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value"
   ```
3. **반영 확인** — `flint hot --session <id>` 출력이 새 상한에서 절단되는지,
   `flint status`의 `hot_p95`가 상한 아래로 유지되는지 확인한다.

## 2. 임계값 오버라이드 (§15 이연결정 "임계값 수치 운영 중 조정" 회수)

모든 튜닝값은 코드 기본값(`lib/store/thresholds.go` 단일 소스) + 이 표의
meta 키 오버라이드로만 조정한다 — 개인 튜닝값을 코드·문서에 커밋하지
않는다(저장소 규칙). v0.16.3까지 하드코딩됐던 소비부 상수
(suggest·conflict·backup-age·zero-near·pending-age)도 이 표로 통합됐다.

오버라이드 관리는 `flint tuning`(v0.23.0) — `list`·`get <key>`·
`set <key> <value>`·`reset <key>`. set은 실경로 파싱으로 검증하며(무효
값·폴백 키의 0은 거부), 개인 튜닝값은 이 경로로 운영 DB에만 둔다.

| meta 키 | 기본값 | 의미 | 경고 발동 |
|---|---|---|---|
| `pending_max` | 50 | 미기록(pending) 결정 상한 | maintain --check |
| `dup_susp_max` | 20 | 중복의심 결정 상한 | maintain |
| `conf_floor` | 0.8 | 확신 하한(0~1) | maintain |
| `stale_sessions` | 3 | 연속 미기록 세션 수 | done |
| `stale_days` | 90 | 미참조·미갱신 스테일 판정 기준(일) — Confluence 스테일 스캔 표준·분기 리뷰 관례 | maintain --stale |
| `stall_hot_h` | 48 | hot 부재 경고 grace(시간, Healthchecks.io nodata 준용) | doctor·maintain --check |
| `stall_write_h` | 72 | write 부재 경고 grace(시간) | doctor·maintain --check |
| `suggest_min_score` | 0.15 | zero_near 근접 판정 유사도 하한(0~1) — 미만 행은 무관 후보로 제외. 제안(did-you-mean) 출력은 v0.14.0 폐지, 지표 분류만 담당 | read(제로히트 스캔) |
| `suggest_scan_cap` | 5000 | 제안 스캔 행 상한 — 이상 규모 유입 시 무상한 스캔 방어 | read |
| `conflict_dup_threshold` | 0.6 | 모순/중복 분류 유사도 기준(0~1, 이상=중복·미만=모순). doctor conflicts도 같은 기본값 소스를 쓰나 오버라이드 적용 경로는 maintain | maintain --conflict-suspects |
| `backup_age_warn_h` | 72 | 백업 신선도 경고 시간(h) — 이상 경과 시 warn | doctor backup-age |
| `zero_near_min_read` | 10 | zero_near 판정 표본 하한(7일 read 합산) — 미만이면 판정 보류 | doctor zero-near |
| `zero_near_warn_pct` | 30 | zero_near 경고 비율(%) — 이상이면 어휘 불일치 의심 warn | doctor zero-near |
| `pending_age_days` | 7 | pending 최고령 경과 안내 기준일 — 이상 경과 시 warn+만료 안내 | doctor pending-age |
| `recall_warn_writes` | 3 | 회상 부재 경고 기록 수 임계 — 오늘 read 0회 상태의 write 이상 시 done 종료 경고·다음 SessionStart 안내. **-1이면 끔**(1로 설정 시 기록마다 경고). 0은 미설정과 같게 기본값 폴백 | done·nudge |
| `dead_days` | 30 | dead_pct 기준일 — 회상 0·이 기간 경과 지식 비율(증류·중복 사이클이 주 단위라는 근거) | report effect |
| `pair_window_minutes` | 30 | read→write 페어 판정 창(분) — 같은 세션 배정 내 근접성 상한 | report effect |
| `chain_window_hours` | 24 | 사용 연쇄 판정 창(시간) — 반환 id 재반환·같은 topic write 인정 범위 | report effect |
| `events_retention_days` | 14 | read_events·session_starts 보존일(daily 카운터 정책 승계) | 계측 정리 |
| `hot_events_retention_days` | 30 | hot_events 보존일(dead_pct 노출 가드 창과 정렬) | 계측 정리 |
| `events_volume_warn` | 50000 | 계측 3테이블 합계 **방어 상한**(대량 유입 안전장치 — 일반 운영 도달 전제 아님, v0.24.0 표기 정정) | doctor event-volume |
| `backup_interval_h` | 24 | 자동 백업 최소 주기(시간) — backup_keep과 한 세트(v0.24.0) | done 자동 백업 |
| `vocab_tags` | (빈) | 개인 tag 어휘(콤마 목록) — 기본 7종과 **합집합**, repo 아닌 운영 DB에(v0.24.0 R4) | write 어휘 경고 |
| `vocab_domains` | (빈) | 개인 domain 어휘(콤마 목록) — 기본 4종과 합집합 | write 어휘 경고 |
| `session_knowledge_days` | 1 | 세션 지식화(세션 종료 요약 pending 적립) 최소 간격(일) — **-1이면 끔**·0은 미설정 취급(기본값 폴백) | done·compact 세션 지식화 |
| `secrets_scan_row_limit` | 10000 | doctor secrets 1회 스캔 행 상한(대형 DB 자원 방어 — 도달 시 부분 스캔 명시, v0.26.1) | doctor secrets |
| `rrf_k` | 60 | 검색 폴백 RRF 순위 완화 계수 — 낮을수록 상위 신호 지배(v0.25.0) | read 폴백 재랭킹 |
| `daily_retention_days` | 14 | daily:* 카운터 보존·기간화 창 상한 — report/status 창·PruneDaily 단일 소스(v0.24.0 — 3곳 하드코딩 통합) | 보존·창 |
| `drill_max_days` | 30 | 백업 드릴 경과 보고 기준일 — 초과 시 `,"drill":"stale"` | maintain --check |

갱신(미설정 키는 기본값, 즉시 재적용 — 재시작 불필요):

```sh
psql "$FLINT_DSN" -c "INSERT INTO meta (key, value) VALUES ('pending_max','40')
  ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value"
```

확인: `flint maintain` 보고의 Counts vs Thresholds 비교가 새 값을 따르는지,
`flint done --session <id>` 경고 기준이 바뀌는지.

### DSN 설정 파일(config.json) — v0.7

임계값은 meta 테이블이지만, DSN은 설정 파일로 오버라이드한다
(`UserConfigDir()/flint/config.json`, `{"dsn": "..."}` 형식). 위치 재배치는
환경변수 `FLINT_CONFIG_DIR`로 지정(gh `GH_CONFIG_DIR`·Dolt `DOLT_ROOT_PATH`
모델 — 마커 파일도 같은 디렉터리에 둔다).

우선순위(높은 순):

1. `FLINT_DSN` 환경변수
2. `config.json`의 `dsn` 키
3. 기본값 `postgresql://flint:flint-dev@localhost:5434/flint`

파일 파손·빈값·비문자열 `dsn`은 기본값으로 폴백한다(에러 아님 — 무효 meta
값 폴백과 동일 원칙). 5434 포트를 다른 컨테이너가 점유해 이동이 필요하면
(`flint doctor --check port`가 점유자를 보고) 이 우선순위로 `FLINT_DSN` 또는
`config.json`을 갱신하고 compose 포트 매핑을 함께 조정한다.

## 3. 실측 예시 (가상 수치)

| 주차 | hot_calls | hot_p95 | 조치 |
|---|---|---|---|
| 1주차 | 38 | 1,780자 | 관측만 |
| 2주차 | 41 | 1,960자 | 상한 2,000자 근접 — `hot_budget` 2,400 갱신 |
| 갱신 후 | 12 | 2,310자 | 절단 0건 — 예산 적정 |

## 4. backup 로테이션 (backup_keep)

`flint backup --out` 성공 시 같은 디렉터리의 덤프를 로테이션한다(v0.5 T54):

- 대상은 `.sql` 파일, **mtime 최신순**으로 `backup_keep`개 유지.
- 나머지는 삭제하며 짝이루는 `.sha256` 사이드카가 있으면 함께 삭제
  (사이드카가 없는 파일은 단독 삭제). 요약 JSON의 `removed`에 개수 출력.
- 기본값 **14**. `backup_keep`이 1 미만이거나 무효하면 기본값 폴백,
  로테이션 자체도 keep 최소 1을 보장(전체 삭제 없음).
- 갱신(즉시 재적용 — 매 백업 실행마다 읽음):

```sh
psql "$FLINT_DSN" -c "INSERT INTO meta (key, value) VALUES ('backup_keep','7')
  ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value"
```

## 5. 기타 meta 키

- `exposure_profile` = `normal` | `minimal`(기본 normal) — 에이전트 노출
  축소 스위치: minimal은 hot 헤드라인에서 회상 힌트 제거·nudge 출력
  전면 억제(마커는 자동 종결). 설정: `flint setup --exposure minimal`
  또는 `flint tuning set exposure_profile minimal`(v0.24부터 tuning
  커버).

- `schema_version` — CLI 호환 검사 전용(수동 갱신 금지).
- `last_backup` — 자동 백업 판정 전용(`flint backup`이 갱신).
- `hot_stats` — hot 계측 영속 누적(`flint status`가 읽고 종료 시 갱신).

## 6. 이연결정 회수 계획 요약

§15 이연 4건 중 이 문서가 회수하는 것은 ①주입 예산 상한(hot_budget)과
④임계값 수치다. ②보존·영구삭제·③백업 포맷은 데이터 축적·pg_dump 검증
이후 별도 결정한다.

## 7. 시간여행·스키마 v5 (v0.8)

### as-of 스펙 3종 (`flint read --as-of` · `flint status --as-of`)

| 형식 | 예 | 정규화 |
|---|---|---|
| ISO 시각(RFC3339·`2006-01-02T15:04`) | `2026-10-24T09:00` | 해당 시각 직전 최신 커밋 |
| 상대 | `72h`·`7d` | now-N 직전 최신 커밋(d는 h×24) |
| 16진 커밋 해시 접두어(3자 이상) | `abc12` | dolt_log에서 1건으로 확정된 전체 해시(0건·2건 이상 오류) |

- 무효 스펙(예: `2 days ago`·2자 접두어)·첫 커밋 이전 시각은
  **exit 3**(오류 문구에 첫 커밋 시각 포함).
- 시간여행 질의는 항상 커밋 해시로 정규화된 뒤 실행된다.

### 무리텐션 — 첫 커밋까지 보존

- as-of 조회는 dolt 커밋에 의존한다. Dolt의 GC·테이블파일 정리로
  참조 커밋이 사라지면 해당 시점 조회가 불가능해진다.
- 따라서 **첫 커밋(초기 적립)까지 보존**을 권장하며, 리텐션을 줄일
  때는 `flint audit`로 첫 커밋 해시를 확인한 뒤 그 이전 커밋만
  대상으로 삼는다.

### 스키마 v5 — `superseded_by`

- knowledge 테이블에 `superseded_by int` 컬럼 1개 추가
  (`meta.schema_version`='5'). 승격은 store 연결 커맨드마다 도는
  스키마 게이트 체인이 자동 수행한다 — 기존 4 이하는 첫 커맨드
  실행 시 승격(다운그레이드는 게이트가 차단).
- `flint maintain --supersede OLD --by NEW`가 OLD에 NEW의 id를
  기록한다(무효화 시각 = NEW의 valid_at). 수동 갱신 대상이 아니다.

## 8. v0.10.1 상수 — v0.16.3부터 §2 표의 meta 키로 레지스트리화

세 상수의 기본값·근거(조정은 §2 표의 meta 키 오버라이드로 — 코드
기본값 직접 수정은 아키텍처 위반, 2026-11-07 패치 루프 3차 정정):

| 상수 | 기본값 | 근거 |
|---|---|---|
| `backupAgeWarn` | `72h` | 자동 백업 24h 주기 기준 3회분 여유 — 백업 2회 연속 실패까지는 경고하지 않고, 3회째 실패(나이 72h 도달)부터 경고 |
| `suggestMinScore` | `0.15` | 무관 top 제안 관측 차단 — 2026-10-30 스모크에서 "환경설정 초기화 방법" 질의에 "flint v0.9.6 방향"이 top으로 제안되던 사례 기준 |
| `conflictDupThreshold` | `0.6` | 모순/중복 후보 분기 문턱 — 동일 topic 요약 유사도(BigramDiceMultiset·bigram 다중집합 Dice, lib/query — suggest 제안 점수의 집합판 BigramDice와는 다른 측정) 0.6 이상은 기존 중복 후보, 미만은 모순 후보(maintain --conflict-suspects) |

조정 지침:

- `backupAgeWarn` — 백업 주기 변경 시 주기×3을 유지한다. 잦은 허위
  경고면 여유를 늘리고, 장애 감지 지연이 문제면 줄인다.
- `suggestMinScore` — 유효 제안이 누락되면 낮추고, 무관 제안이 다시
  관측되면 높인다. 제안 라인은 폐지돼(§9) top 제안을 직접 볼 수 없으므로
  `flint report`의 `zero_near_pct`로 관측한다 — 근접 지식이 있는 제로히트
  질의가 zero_near에 안 잡히면 임계가 높은 것(낮춘다), 무관 지식까지
  잡히면 낮은 것(높인다).

## 9. zero_near — 제로히트 침묵 계약 계측 (v0.14.0)

`flint read`는 재검색(fallback)까지 제로히트면 무출력·exit 1로 침묵한다
(제안 출력 폐지 — 근접 후보는 에이전트에 노출하지 않는다). 대신 근접
후보 유무를 `zero_near` 지표로 내부 계측해 report가 노출한다.

- exit 1 구분 — exit 1은 저장소 오류(MapExit 기본)와 공존한다: 무출력
  exit 1이 침묵 계약(정상), 오류 JSON 한 줄을 동반한 exit 1이 실패다.
- 분류 임계 — 제안 스캔 `suggestMinScore` `0.15`를 그대로 재사용한다
  (별도 상수 없음). 최고 근접 점수가 0.15 이상인 제로히트를 zero_near로
  분류한다.
- 해석 — `zero_near_pct` = 검색 품질 문제 비율(지식은 있으나 질의가
  못 찾음), `zero_pct − zero_near_pct` = 지식 부재 비율(추가해야 할
  지식). zero_near_pct가 높으면 임계·색인을 점검하고, 낮으면 지식
  축적이 우선 과제다.

## 10. 기간화 지표·doctor 임계·계측 왕복 계약 (v0.15.0)

### 기간화 지표 — Σ daily 합산 산출

`flint report`의 `zero_pct`·`zero_near_pct`는 누적 `read_stats`가 아니라
`--days` 창의 일별 `daily:YYYY-MM-DD` 행 합산(Σread_zero·Σread_zero_near
/ Σread, 버림 정수%)으로 산출한다. 누적 `read_stats`는 임계 변경 이력·
스키마 전환 등으로 오염될 수 있어 지표 원천으로 쓰지 않는다(절연) —
일별 키는 PruneDaily가 14일만 보존하므로 창도 그 범위 안에서만 유효하다.

### doctor zero-near 임계

`flint doctor`의 zero-near 검사는 최근 7일 daily 합산에서 판정한다:

| 상수 | 기본값 | 의미 |
|---|---|---|
| 표본 하한 | `10` | 창 내 Σread가 10 미만이면 표본 부족으로 ok(판정 보류) |
| 경고 임계 | `30`% | Σread_zero_near×100/Σread ≥ 30이면 warn — 어휘 불일치 의심, 검색 품질 개선 여지 |

경고는 동작을 막지 않는다(warn — fail 아님). 표본 하한·경고 임계는
§2 표의 meta 키(`zero_near_min_read`·`zero_near_warn_pct`)로 조정한다.

### 계측 왕복 계약 — 단일 라이터·병합 upsert

read 경로 계측은 왕복 예산(본 쿼리 제외 ≤ 5회)을 지킨다:

- **단일 라이터** — `read_stats`는 BumpReadStats 하나가 단일 읽기-수정-
  쓰기로 calls·zero·zero_near를 한 번에 갱신한다(부분 갱신 금지).
- **병합 upsert** — daily 행과 `last_read_at`은 단일 2행
  `INSERT ... ON CONFLICT DO UPDATE`로 함께 쓴다(왕복 1절감).

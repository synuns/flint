# RUNBOOK — flint 운영 절차

감사·오염 대응·복구 훈련·백업 보존·진단·스테일 관리·유지보수 주기의
8절 구성. 모든 절차는 각 커맨드 계약(exit 0/1/2/3, 한 줄 JSON)을 따른다.

설치·설정은 README 설치 절을 따른다(4경로 — install.sh 원라인·
go install·수동+체크섬 검증·Homebrew cask 가이드):
[README.md — Installing the binary](../README.md#installing-the-binary)

## 1. 감사 — `flint audit`

- `flint audit [--limit N] [--session ID]` — dolt_log 최근 커밋을
  `commit_hash`·`committer`·`date`·`message`와 함께 한 줄 JSON으로 출력한다.
- `--limit` 기본 20, 상한 100.
- 특정 세션만 보려면 `--session ID`.

## 2. 오염 대응 — 감지 → 격리 → revert → 재감사

1. **감지**: `flint audit`로 의심 커밋(`commit_hash`)을 확인한다.
2. **격리**: 해당 세션 쓰기를 중단시키고 원인 세션을 고정한다.
3. **revert**: psql에서 되돌린다.

   ```
   psql "$FLINT_DSN" -c "CALL dolt_revert('<commit_hash>')"
   ```

4. **재감사**: `flint audit`를 다시 실행해 revert 커밋까지 확인한다.

## 3. 복구 훈련 — `flint restore --drill` (30일 주기)

- `flint restore --file <file> --drill` — 덤프 파일을 임시 데이터베이스
  `flint_drill_db`에 재생하고 행수를 대조한다(v0.15.2 — DB 수준 격리,
  종료 시 항상 삭제). 결과는 `maintenance_runs`에 기록되고
  `meta.last_drill`이 갱신된다.
- `flint maintain --check`가 30일 경과를 보고한다. 경과 시 최신 덤프로
  drill을 반복한다. 주기: **30일마다 1회**.

## 4. 백업 보존 — keep-N · 암호화 (v0.28.0)

- `flint backup` — `meta.backup_keep`(기본 14)에 따라 오래된 덤프와
  sha256 사이드카를 함께 삭제한다(keep-N). `.sql`(평문)·`.sql.age`
  (암호문) 모두 로테이션 대상.
- 보존 수는 `meta` 테이블의 `backup_keep` 값으로 조정한다(기본 14).

### 백업 위치 원칙

- 백업은 repo 밖(`~/.flint/backup`)이 원칙 — 개인 DB 전문 덤프를 git
  저장소에 두면 커밋·동기화 유출 경로가 된다. `--out`이 git 저장소
  내부면 flint가 경고하고(v0.28.2), 이 repo는 .gitignore·가드가
  덤프 형상(자동 명명·매직 헤더)을 반입 차단한다.

### 백업 암호화 (옵트인)

- 켜기: `flint tuning set backup_encrypt true`(또는 1회성
  `flint backup --out <경로> --encrypt`). 키 파일
  `인프라 데이터 루트 FLINT_HOME의 backup.key(기본 ~/.flint, FLINT_HOME env 오버라이드 · `flint doctor --check backup-key`가 실제 경로 표시)`(0600)가 첫 암호화 시 자동 생성된다.
- **키 분실 = 암호화 백업 해독 불가** — 키 파일은 패스워드 매니저 등에
  별도 사본을 보관한다. 기기 교체 시 키 파일 이전이 먼저다.
- 위협 모델: 백업 파일이 기기를 벗어나는 유출(클라우드 동기화·복사·
  업로드 사고) 방지. 키까지 함께 탈취되는 기기 전체 침해·로컬 권한
  상승은 방어 대상이 아니다(로컬 단일 사용자 전제).
- 복호은 `flint restore`가 형식 감지 후 자동 수행(`--drill` 동일) —
  키 부재 시 명확 에러로 차단된다.
- 진단·공유용 마스킹 덤프(`flint backup --mask --out`)는 비밀 패턴을
  치환한 비복원 아티팩트다(사이드카 미부여·마커 이중 방어).
- doctor `backup-key` 체크: 암호화 켜짐인데 키 부재·권한 이완 경고.

## 5. 진단 — `flint doctor` (v0.6 → v0.16.2 확장)

검사 16종 + 설치 환경 조건부 하니스·스킬 검사(brew doctor 준용 — `--list-checks`로 목록, `--check NAME`으로
서브셋 실행, 무효 검사명은 exit 3). 코어 6종 + tail-chain 10종 — 표 순서는 doctor 실행 체인 순서:

| 검사 | 내용 | 수정 명령 | fail/warn |
|---|---|---|---|
| hook-file | Claude 설정·플러그인 캐시에 flint 훅 등록 여부 (`FLINT_CLAUDE_HOME` 오버라이드 지원) | 플러그인 (재)설치(`/plugin marketplace add synuns/flint`) 또는 `FLINT_CLAUDE_HOME` 경로 확인 | fail |
| db | DB Ping 접속 | `docker compose up -d` 후 재실행(`FLINT_DSN` 확인) | fail |
| schema | meta schema_version ≥ 4 일치(현재 v10) | `flint setup`(마이그레이션) 후 `flint doctor --check schema` | fail |
| integrity | `flint_knowledge_*` 인덱스 3행 존재 | `flint status`로 인덱스 승격 재시도(최신 백업 있으면 §4 복구 검토) | fail |
| last-seen | 3경로(write/hot/read) last-seen 경과 + grace(stall_hot_h 48h·stall_write_h 72h) | 세션 시작해 `flint hot`/`flint write` 1회 실행 | warn |
| port | 호스트 5434 포트 점유 충돌(타 프로세스 점유 시 이동 안내) | `FLINT_DSN` + compose 포트 오버라이드로 이동(TUNING §2) | fail |
| backup-age | 최신 백업 경과(backup_age_warn_h 기본 72h 초과 시) | `flint backup`(§4 keep-N 정책) | warn |
| conflicts | 모순 후보 그룹 1+ 탐지(conflict_dup_threshold 기준 중복/모순 분류, 조회 상한 50그룹) | `flint maintain --conflict-suspects` 후 §6 정리 | warn |
| zero-near | 제로히트 중 근접 후보 존재 비율(어휘 불일치 신호, zero_near_min_read 이상 표본에서) | 태그·topic 표현 정리·TUNING §2 임계 점검 | warn |
| pending-age | pending 최고령 경과(pending_age_days 기본 7일 초과 시) — 만료 안내와 연계 | `flint maintain --expire-pending N [--commit]`(§6) | warn |
| event-volume | 계측 3테이블(read_events·session_starts·hot_events) 합계 행 수가 events_volume_warn(기본 50000) 이상 | 보존일(events_retention_days·hot_events_retention_days) 점검(TUNING §2) | warn |
| backup-key | backup_encrypt 켜짐인데 키 파일 부재·권한 이완(0600 아님) — 키 재생성 시 기존 암호문과 호환 불가 혼재 위험 | 기존 키 위치 확인 후 `flint backup --encrypt --out <경로>` · 권한은 `chmod 600 <키>` | warn |
| meta-hygiene | 미지 meta 키(사용 중단 후보)·보존 창(daily_retention_days)+7일 유예 초과 daily 카운터 | `flint maintain --meta-audit` 확인 — 낡은 키·daily 수동 정리 | warn |
| update | 최신 릴리스 대비 현재 빌드 지연(5초 타임아웃 네트워크 확인, 오프라인은 판정 보류) | `flint update` | warn |
| recall-starved | 회상 부재 세션 누적 카운터(recall_starved_count, 프로파일 무관 — 최후 관측 창구) | 다음 세션 시작 전 `flint read` 1회 — 해소 후 psql로 meta 키 삭제 | warn |
| secrets | 보유 지식·세션 사후 비밀 스캔(무효화 행 제외, 행 상한 secrets_scan_row_limit — 도달 시 부분 스캔 명시) | 지식: `flint maintain --invalidate --id N --reason secret` · 세션: 해당 agent 재종료(done) | warn |

- **fail만 exit 1**, warn은 exit 0. fail마다 수정 명령 한 줄이 함께 출력된다.
- 경고 임계값은 meta 키로 조정(TUNING.md §2 — `stall_hot_h`·`stall_write_h`·
  `backup_age_warn_h`·`zero_near_warn_pct`·`pending_age_days` 등).

fail 시 대응:

1. **hook-file** — 출력된 fix 문구의 안내 설치 커맨드를 실행한다
   (플러그인 설치 — `/plugin marketplace add synuns/flint` — 또는
   `FLINT_CLAUDE_HOME` 경로 확인). 훅 없이도 flint CLI는
   동작하므로 세션은 즉시 복구 필요 없음.
2. **db** — `docker compose up -d`로 Doltgres 기동 후 재실행.
   DSN은 `FLINT_DSN` 확인(기본 `localhost:5434`).
3. **schema** — `flint setup`(마이그레이션) 실행 후 `flint doctor --check schema`
   로 재확인.
4. **integrity** — 인덱스 재생성 필요. 최신 백업 덤프가 있으면 §4 절차로
   복구 검토, 없으면 `flint backup` 후 대응.
5. **port** — 5434를 타 프로세스가 점유. fix 문구의 이동 안내대로
   `FLINT_DSN` + compose 포트 오버라이드로 조정.
6. **last-seen(warn)** — hot 48시간·write 72시간 부재. 세션을 시작해
   `flint hot`/`flint write`를 한 번 실행하면 해소된다(지속되면 훅 등록
   상태를 hook-file 검사로 점검).
7. **backup-age(warn)** — `flint backup` 실행으로 갱신(§4 keep-N 정책).
8. **conflicts(warn)** — `flint maintain --conflict-suspects`로 후보를
   확인하고 §6 무효화/병합 경로로 정리한다.
9. **zero-near(warn)** — 지식은 있는데 검색이 놓치는 상태(어휘 불일치).
   태그·topic 표현을 다듬거나 TUNING §2 임계를 점검한다.
10. **pending-age(warn)** — 만료 기준 초과 pending은
    `flint maintain --expire-pending N [--commit]`으로 정리한다(§6).

## 6. 스테일 관리 — `flint maintain --stale` (v0.6 → v0.16.2 확장)

- `flint maintain --stale [--days N] [--limit M]` — active 항목 중
  (last_read_at < now-N일) 또는 (read_count=0 AND ingested_at < now-N일)
  항목을 보고한다. `--days` 기본은 meta `stale_days`(기본 90),
  `--limit` 기본 20.
- 미참조 버킷(read_count=0)은 never_read로 구분 표시한다.
- **`--stale`·`--invalidate-stale`(dry-run)은 보고 전용·exit 0** —
  물리 삭제·자동 confidence 변경 없음. 무효화(=가역)만 수행한다.
- 결과의 `days90`·`never_read` 수치는 `flint report`의 stale 섹션과
  상호 확인한다.

v0.8 무효화 경로(항상 dry-run 먼저):

1. **대상 확인(dry-run)** — `flint maintain --invalidate-stale`:
   대상 목록(id·사유 stale|never_read)과 요약행 "N to invalidate"를
   출력한다(`--days`·`--limit` 플래그는 `--stale`과 동일).
2. **실행** — `flint maintain --invalidate-stale --yes`: dry-run과 같은
   조건의 대상 전체를 `status='invalidated'`로 무효화한다(프롬프트
   없음 — `--yes`가 유일한 게이트).
3. **id 스냅샷 적용** — dry-run 목록의 일부만 처리하려면
   `flint maintain --invalidate --ids "1,2,3" --yes`. dry-run 시점의
   id를 고정 적용하므로 재평가로 대상이 달라져도 영향 없다.
   존재하지 않는 id는 exit 1. 기존 단일 `--id N`과 병존하며
   `--ids`가 우선이다.
4. **선행 관계 무효화** — `flint maintain --supersede OLD --by NEW`:
   OLD(active인 경우만)를 무효화하고 `superseded_by=NEW`를 기록한다.
   무효화 시각은 NEW의 valid_at을 따른다. NEW 부재 → exit 1·
   OLD==NEW → exit 3·OLD 비active → exit 1.
5. **만료 pending 정리** — `flint maintain --expire-pending N`:
   N일 초과 경과 pending을 만료(invalidated)한다. 기본 dry-run,
   `--commit`으로 실행(doctor pending-age 경고의 정리 경로).
   일수는 모드 값으로 받는다(`--days` 아님).
6. **invalidated 정리 + 회상 이벤트 보존기한** — `flint maintain
   --prune-invalidated N`: N일 초과 경과 invalidated 지식 삭제와 함께
   `recall_event_retention_days`(기본 90일) 만료 회상 이벤트를 정리한다
   (append-only 원천 recall_events의 구조적 성장 상한 — dry-run은
   `would_prune_recall_events` 보고). 지식 삭제 대상의 이벤트·이력
   고아도 같이 정리된다. `--commit`으로 실행.

## 7. 유지보수 주기 — 스케줄 실행 가이드 (v0.8)

스테일 판정 기본이 90일(stale_days)이므로 **저빈도 실행을 권장**한다 —
월 1회면 충분하며, 일 단위 이하의 고빈도는 무의미한 커밋만 늘린다.
스케줄러는 **dry-run(`--check`·`--invalidate-stale` 마커 없음)만**
실행하고, `--yes` 적용은 운영자가 dry-run 출력을 보고 수동으로
판단한다.

흐름(매월):

1. `flint maintain --check` — 백업·drill·스톨 경과 확인.
2. `flint maintain --invalidate-stale` — dry-run으로 대상 목록·
   "N to invalidate" 확인.
3. 운영자 판단 후 `flint maintain --invalidate-stale --yes`(또는
   `--ids`로 선별 적용).
4. `flint maintain --expire-pending 30` — 만료 pending dry-run 확인 후
   판단 시 `--commit`(doctor pending-age 경과 시).

cron 예(매월 1일 04:00, dry-run만 — `make install`은 `$(go env GOPATH)/bin/flint`에 설치하므로 그 경로를 쓴다):

```
0 4 1 * * FLINT_DSN=postgresql://flint:flint-dev@localhost:5434/flint \
  $(go env GOPATH)/bin/flint maintain --check >> "$HOME/.flint/maintain.log" 2>&1
```

launchd 예(`~/Library/LaunchAgents/dev.flint.maintain.plist` — plist는 변수 전개를 하지 않으므로 절대경로를 적는다. 기본 GOPATH라면 `$HOME/go/bin/flint (plist에서는 실제 경로)`):

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>dev.flint.maintain</string>
  <key>ProgramArguments</key>
  <array>
    <string>YOURHOME/go/bin/flint</string>
    <string>maintain</string>
    <string>--check</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>FLINT_DSN</key>
    <string>postgresql://flint:flint-dev@localhost:5434/flint</string>
  </dict>
  <key>StartCalendarInterval</key>
  <dict><key>Day</key><integer>1</integer><key>Hour</key><integer>4</integer></dict>
  <key>StandardOutPath</key><string>/tmp/flint-maintain.log</string>
  <key>StandardErrorPath</key><string>/tmp/flint-maintain.log</string>
</dict></plist>
```

등록: `launchctl load ~/Library/LaunchAgents/dev.flint.maintain.plist`.
경과 보고는 `flint report`·§3 drill 주기(30일)와 상호 확인한다.

## 8. 실DB 파손 복구 — AS OF 선택 복원 (v0.9.4)

v0.9.3 빌드 중 실DB wipe 사고에서 실증한 복구 절차다. §2의 revert
절차로 되돌리지 못하는 충돌 상황(아래 원인)에서 이 절을 따른다.

- **증상**: `knowledge` active 행수가 급감하고, `flint audit`의
  `dolt_log` 커밋 이력과 실제 행수가 불일치한다.
- **원인**: 커밋되지 않은 raw `DELETE`(작업집합)를 후속
  `dolt_add --all` 커밋이 함께 흡수해, 파괴가 커밋 이력 안으로
  들어간 상태. revert 대상 커밋에 무관 변경이 섞여 충돌이 난다.

**절차 5단계**(실례: `jkfa904` 파괴 → `001nnpn` 복구, 2026-10-28):

1. 작업집합 확인 — psql에서 `dolt_status`로 미커밋 변경을 조사한다.
2. 정크 정리 + 체크포인트 커밋 — 남은 정크(빌드 잔여물 등)를 버리고
   작업집합을 비워 체크포인트 커밋한다.
3. `dolt_revert` 시도 — 파괴 커밋을 되돌린다(§2와 동일).

   ```
   psql "$FLINT_DSN" -c "CALL dolt_revert('<파괴 커밋>')"
   ```

4. 충돌 시 **선택 복원** — revert가 충돌로 막히면, 직전 정상 커밋의
   스냅샷에서 대상 행만 복원한다(선택 복원).

   ```sql
   INSERT INTO knowledge SELECT * FROM knowledge
     AS OF '<직전 정상 커밋>'
     WHERE id IN ('<id1>', '<id2>', …);
   ```

5. 검증 — `flint read`(또는 SQL 조회)로 행 복원을 확인하고
   `dolt_status`로 작업집합이 비었는지 확인한 뒤 커밋한다.

- **세션 테이블 유실은 수용**한다(PLAN §8 — 세션은 재수집 대상).
- **정크는 미복원 원칙**: 복구 범위는 유효 지식(active) 행만으로
  한정하고, 빌드 정크는 되살리지 않는다.

## 8a. sync 패닉 회복 — reset --to-remote (v1.7.2)

다중 기기를 동시에 연결해 `flint sync pull`이
`DoltgresHandler caught panic` 오류로 실패할 때의 회복 절차다
(v1.7.2 시점 기록. v1.9.0부터 **다중 기기 지원**: 회상 이벤트는
append-only라 기기 간 자동 병합되고, 동일 지식 행 쓰기 충돌은
기존 working-set 보존 계약을 따른다).

1. 다른 기기의 flint 사용을 중단한다(단일 활성 기기 원칙).
2. 손실 경고를 확인한다 — reset은 로컬 브랜치를 원격 `main`으로
   되돌리므로 **푸시하지 않은 로커커밋이 폐기**된다. 필요하면
   먼저 `flint sync push <remote>`로 보존한다.

   ```
   flint sync reset --to-remote <remote> --yes
   ```

3. 검증 — `flint audit`로 커밋 이력을 확인하고 `flint doctor`로
   건전성을 점검한다.

## 동시 쓰기 수용 계약 (v1.6.2 — R33 E3·E4 문서화)

두 항목은 결함이 아니라 수용된 계약이다(원 보고 기준 문서화 수용분):

- **병렬 write의 빈 커밋·보고 hash(E3)** — 동일 내용을 두 라이터가
  동시에 write하면, unique 충돌 분류 후 살아남은 패자의 줄은 이기는
  쪽 커밋에 이미 반영된다. 이때 패자의 커밋이 빈(행 변화 없는)
  커밋로 남을 수 있고, 출력되는 `hash`는 마지막 커밋을 참조한다 —
  **데이터 유실은 없다**(패자의 고유줄은 survive 경로로 잔존).
- **플랫 키 동시 증분의 LWW 유실(E4)** — daily 카운터
  (`meta`의 `daily:<kind>:<date>` 플랫 키) 증분은 last-write-wins
  upsert라 동시 증분 하나가 유실될 수 있다. 관측 통계(report·status)
  용도로만 쓰이고 정산 자료가 아니므로 감수한다(구조 개선은
  per-table 분리 — v1.7 과제, PLAN 비목표 E2 참조).

## 스키마 v10 마이그레이션 (v1.2.0)

- `flint setup`이 additive 마이그레이션을 적용한다 — read_events 신규
  열(bytes·zero_reason·source)·`knowledge_reads` 테이블.
- daily 카운터는 플랫 키(`daily:<kind>:<date>` 정수값)로 전환되었다 —
  구 `daily:<date>` JSON 키는 읽기 폴백으로만 승계되므로 기존 창 통계
  손실 없이 `flint report`가 그대로 이어진다(보존 14일 불변).

## 테스트 DB 장기 운영 위생 (v0.21.2)

`make e2e`가 Doltgres 엔진 내부 패닉(interface conversion류)으로 실패하면
공유 `flint_test`의 **커밋 이력 누적**이 원인일 수 있다 — 모든 쓰기가
Doltgres 커밋을 만들어 이력이 무한히 자라며, 오래된 DB에서 플래너
버그를 건드릴 수 있다(2026-11-07 실측 사례). 해결:

```sh
psql postgresql://flint:flint-dev@localhost:5434/flint \
  -c "DROP DATABASE flint_test" -c "CREATE DATABASE flint_test"
make e2e   # 신규 DB에서 게이트가 스키마를 재구축한다
```

운영 DB(`flint`)는 해당 없다 — 이력은 시간여행(as-of) 기능의 자산이다.

## 비밀 스캔 오탐 완화 — allowlist (v0.26.1)

문서용 예시 토큰·프로젝트 고유 접두사가 비밀 패턴에 오탐되면
`~/.config/flint/secrets-allowlist.txt`(repo 밖, 한 줄 하나, `#` 주석)
에 등록한다 — 등록 문자열은 쓰기 경고·doctor secrets 모두에서 제외된다.

```sh
mkdir -p ~/.config/flint
echo 'ghp_example_token_for_docs_12345' >> ~/.config/flint/secrets-allowlist.txt
```

## 백업 권한 경화(v0.20.6) 호환성 주의

백업 덤프·사이드카는 0600, 디렉터리는 0700이다. 다른 사용자로 실행되는
동기화·크론 잡이 `~/.flint/backup`을 읽고 있었다면 v0.20.6부터 접근
거부된다 — 해당 잡을 동일 사용자로 옮기거나 권한을 명시 조정한다.

## 비밀 재노출 주의 (v0.26.2 재검수)

doctor secrets가 경고한 지식을 `flint read <질의>`로 확인하면 **본문이
평문으로 재노출**된다(read는 저장 내용 반환 — 설계상 정상). 무효화 전
확인은 최소한으로. 백업 덤프 역시 전량 평문(이연: age 암호화·마스킹 —
마일스톤 F)이므로 덤프 보관 위치 권한(0700)을 유지한다.

allowlist 등록 확인: doctor secrets 출력의 'allowlist N항목' 표시로
적용 상태를 확인한다(0항목이면 파일 미로드·경로 오타 의심 —
FLINT_SECRETS_ALLOWLIST 또는 ~/.config/flint/secrets-allowlist.txt).

## 9. 릴리스 서명 키 등록 (v0.31.0, ADR-0014 — 최초 1회)

전환 모드(현재)에서 하드 검증으로 넘어가는 단계. 비밀키는 이 문서를
읽는 본인만 생성·보관한다(에이전트·세션이 만지면 신뢰 뿌리 오염).

1. 키쌍 생성 — **비밀번호를 설정한다**(프롬프트에 입력, 패스워드
   매니저에 보관. 빈 비밀번호 금지):

```sh
cosign generate-key-pair --output-key-prefix release
```

2. 시크릿 등록(비밀번호는 대화식으로 안전하게) + 공개키 커밋:

```sh
gh secret set COSIGN_PRIVATE_KEY < release.key
gh secret set COSIGN_PASSWORD          # 프롬프트에 1단계 비밀번호 입력
cp release.pub lib/cli/cosign.pub      # 다음 릴리스부터 바이너리가 임베드
git commit -am "chore: 릴리스 서명 공개키 등록 (ADR-0014)"
```

3. 이후 자동 — CI가 checksums.txt에 서명(.sig 업로드), flint update가
   임베드 공개키로 검증. 기억할 것 없음.

키 파일 보관: `release.key`(비밀키)는 repo 안에 두지 않는다 — 생성 후
`~/.config/flint/release.key`(0600)로 이동(위 백업 사본·gitignore
패턴 적용됨). 시크릿이 원본이므로 로컬 사본은 백업용이다.

서명 스텝 실패 시 복구(키 불일치·일시 실패): 태그가 이미 존재해 재실행이
스킵되므로 태그 재push로 서명만 재수행한다 —

```sh
git tag -d v0.X.Y && git push origin :refs/tags/v0.X.Y   # 원격 태그 삭제
git tag v0.X.Y && git push origin v0.X.Y                  # 재push → 서명 스텝 재실행
```

키 유출 시 롤링(ADR-0014): 위 1~2를 새 키로 반복. 구 공개키를 임베드한
기존 설치는 새 키 서명을 거부한다 — 의도된 경보다. 각 기기에서 1회
`flint update --insecure-skip-signature`로 신규 키 바이너리로 넘어간
뒤 이후부터는 새 공개키로 자동 검증된다.

## 실DB 테스트 격리·러너 (v1.0.10 확정)

- 실DB slow 태스크(db·backup·regression 스위트)는 격리 커밋 후 파견 —
  공유 flint_test 경합(40001·flaky)이 재현 경로. 감사 D2 결정.
- Go 워크트리에서 sl run_tests(pytest 기반)는 부적합 — `go test ./...
  -count=1` 직접 실행이 표준(v1.0.1 룰링 승계, 여기에 문서화).
- CI `make ci`는 `-count=1` 고정 — 로컬과 동일 경로 보장.

## 지식화 파이프라인 1단계 — 사용자 교정 채널 (v1.11.0)

- **채널 추가 체크리스트**: source 등록표(lib/knowledge/write.go)에 채널 추가 시
  ① plugin_v06_test.go 미러 목록 ② RecordUsageCount kind("dup_"+source)
  ③ SKILL 소스 표기 ④ lib/ops/report.go dailyFlatFields·ReportDay에
  dup_<채널> 필드를 함께 갱신.

케이스 매트릭스 최우선(사용자 직접 교정)만 실증한 단계다
(원 제안 — docs/plans/2026-11-19-flint-knowledge-pipelines.md).

- **채널 계약**: `flint write knowledge --source correction` — 사용자
  교정을 인지한 에이전트가 즉시 적립한다(스킬 지침 유도). payload에
  confidence 미지정 시 기본 0.7(사람 신호 — hook 0.5·수동 명시 0.8
  사이)·태그 `correction` 자동 부착(기존 태그 보존 병합). 4연산·
  content_hash dedup·pending→approve 승격 회로는 기존 write 회로
  재사용(변경 없음 — 교정 계열도 pending 유지, 자동 active 승격 없음).
- **감지 경로 룰링 — B안(에이전트 계약 유도)**: 하네스 무관·구현 최소로
  plugin 스킬 지침(flint-plugin/skills/flint/SKILL.md)으로 순응을 유도
  한다. 품질 상한은 에이전트 순응도다. **A안(하네스 훅 자동 감지)**은
  프라이버시 표면 문제로 후속 조사 과제(비목표).
- **임계값 2키** — 코드 기본값+meta 오버라이드(TUNING §2):
  `correction_confidence`(기본 0.7)·`correction_daily_max`(기본 20 —
  일일 상한 초과 시 조용히 skip·경고 1행).
- **A안 조사 부록 — 하네스 훅 교정 감지 (v1.12.0, Claude Code 공식
  hooks 계약 기준)**: ① **가용성/추출 지점**: `UserPromptSubmit`가 최적 —
  프롬프트 제출 시점에 전문(`prompt` 필드)이 훅 스크립트 stdin(JSON)으로
  전달되고, exit 0 출력으로 관찰(컨텍스트 주입만, 차단 불필요) 가능.
  `SessionStart`는 세션당 1회라 감지용 부적합, `PreToolUse`/`PostToolUse`는
  도구 호출마다 발화해 표면 과잉, `Stop`의 `last_assistant_message`는
  에이전트 발화라 사용자 교정 아님. 단 `UserPromptSubmit`는 예약 태스크·
  백그라운드 보고에서도 발화하므로 prompt 출처 구분이 전제. ② **프라이버시
  양립**: 전체 발언 캡처 금지 원칙과는 "로컬 필터" 구조로 양립 — stdin에
  전문이 오지만 스크립트가 교정 패턴(정정 관용구) 매칭에 실패하면 즉시
  exit 0·무기록, 성공 시 정정 문구만 추출. flint CLI는 추출물만 수신(전문
  미수신). 훅 실패는 non-blocking(프롬프트는 진행)이라 부작용 없음.
  ③ **권장 아키텍처/난이도**: 훅(플러그인 `hooks/hooks.json`,
  UserPromptSubmit command) → 로컬 필터 스크립트(정규식 매칭·정정 문구
  추출) → `flint write knowledge --source correction`. 구현 난이도 낮음 —
  스크립트+플러그인 번들이고 flint 본체 변경 불필요(교정 채널 재사용).
  ④ **v1.14 판정 기준(안)**: ㉮ 합성 데모 세션에서 패턴 정밀도(오탐 0)
  실증 ㉯ 프롬프트당 지연 무시 가능 수준 확인 ㉰ 훅 디버그 로그에 전문
  잔류 없음 확인 ㉱ B안(스킬 지침) 대비 교정 회수율 우위 실증 — 통과 시
  A안 채택, 미달 시 B안 유지.
- **후속**: A안 구현 판정은 v1.14(위 부록 기준)·회상 실패·반복 패턴의
  적립은 v1.12.0 2단계(아래 절) 구현 — 검증된 성공 케이스(survived)
  강화·A안 등이 잔여. 교정 적립→승격 전환율 관측은
  계량 키 설계 주석만(실측 인용 금지).

## 지식화 파이프라인 2단계 — 회상 실패 격차 후보 (v1.12.0 gap-candidates)

1단계(사용자 교정)에 이어, 회상 실패·반복 패턴 케이스를 자동 적립하는
단계다(원 제안 — docs/plans/2026-11-19-flint-knowledge-pipelines.md의
남은 케이스).

- **감지 계약 — owned_miss 한정**: 지식 read의 0히트 결과는 활성
  지식 소유 여부로 분기(spec §2.2)하며, 그중 **owned_miss**(소유
  지면 위의 검색 실패)만 카운터에 적립한다. `unowned`(DB 공허
  노이즈 — 리서치 룰링)와 hot 경로는 제외. `--as-of`(과거 시점
  검사)도 현재 격차가 아니므로 제외.
- **정규화 해시 카운터 — daily 리셋**: 쿼리 본문은 저장하지 않고
  정규화(NFKC → 80룬 절단[정규화된 형태 기준, v1.12.1 정정] → 살균 →
  소문자 → 공백 접기) 후 sha256 다이제스트로만 다룬다. 카운터 키
  `daily:gapq:<sha256>:<UTC 일자>`는 일별 플랫 키로, **같은 날의
  반복만 누적**되고 날짜가 바뀌면 PruneDaily 리텐션·롤오버로 리셋된다
  (당일 N회 실패가 강신호, 여러 날에 흩어진 실패는 약신호 — 게이트
  리뷰 룰링).
- **임계값·상한 2키** — 코드 기본값+meta 오버라이드(TUNING §2):
  `gap_query_threshold`(기본 3 — 카운터가 임계 도달 시 후보 1건
  적립)·`gap_daily_max`(기본 10 — 신규 ADD만 상한 소모, 동일 해시
  요약 갱신은 미소모[`correction_daily_max` 관례], 초과 시 조용히
  skip·경고 1행).
- **적립 계약 — pending 자동(0.4) → approve 승격**: 후보는
  confidence 0.4(conf_floor 미만 — 항상 pending)로 `gap` 채널에
  자동 적립되고, 명시적 approve로만 승격된다(자기 강화 트랩 차단).
  태그 `gap-candidate`·토픽 `격차 후보: <정규화 쿼리 60룬>`·요약
  `회상 실패 N회`. N-불변 content_hash dedup — 기존 pending 행은
  요약 N만 갱신(UPDATE), active 행은 NOOP.
- **후속**: 검증된 성공 케이스(survived) 강화·교정 전환율 관측은
  계량 키 설계 주석만(실측 인용 금지).

## 지식 수명주기 — age-audit·변동성 계층 (v1.13.0 age-audit)

스테일 관리(§6)의 점수 기반 확장: 낡은 지식을 관측 신호로 **점수화해
보고만** 하고, 처분은 기존 승인제를 그대로 따른다.

- **사용법** — `flint maintain --age-audit [--limit M]`: 상위 목록 JSON
  리포트(status·threshold·count·items·summary). 읽기 전용·dry-run 기본
  (exit 0) — 물리 삭제·자동 무효화 없음. `--limit` 기본 20(decay-audit
  관례). 토픽은 40룬 절단·살균(ADR-0006) 후 출력된다.
- **점수 산식** — `round(100 × (0.7 × min(1, ttl_days / stale_ttl_days)
  + 0.3 × min(1, 동일 topic :correction: 태그 수 / 3)))`. 가중치 0.7/0.3·
  포화 `min(1,·)`·교정 포화 3은 코드 상수(산식 일부 — 튜닝 아님).
  `ttl_days`는 `COALESCE(최근 회상, ingested_at)` 경과일(§6 listStale
  관례). 참조열(비가중) — 회상 후 무효화 이력(bad_recall)·동일 topic
  supersede 이력은 리포트 정보로만 제공된다.
- **임계값 2키** — TUNING §2: `stale_score_threshold`(기본 70 — 이상
  인 항목에 `over_threshold` 마킹)·`stale_ttl_days`(기본 180 — TTL
  포화일). 코드 기본값 + meta 오버라이드.
- **처분 — `--invalidate` 승인 위임**: 신규 실행 플래그(`--commit` 등)는
  없다. 리포트의 id로 `flint maintain --invalidate --ids "1,2,3" --yes`
  (또는 단일 `--id N --reason S`)로 승인 처분한다(§6 3단계 id 스냅샷
  관례 승계).
- **변동성 계층 태그 3종** — CONTENT §4: `vol:immutable`(감쇠 면제 —
  effective=점수 유지)·`vol:conditional`(기존 감쇠 승계, 문서화 마커)·
  `vol:volatile`(기존 감쇠 배율 승계). 태그 없음=일반(기존 동작).
  콜론 포맷은 **사전 등록 태그 전용** — 콜론이 든 사용자 태그 토큰은
  write에서 거부된다(exit 3).
- **immutable 감쇠 면제** — `:vol:immutable:` 지식은 hot·brief 랭킹과
  decay-audit·age-audit 후보에서 감쇠가 면제된다(`EffectiveScoreFor` 단일
  정의 — 순수 식 `EventEffectiveScore` 불변). age-audit 모집단에서는
  서버측 strpos로 처음부터 제외된다.

## 지식화 파이프라인 3단계 — 자동 교정 감지·survived 완화·케이스 등록표 (v1.14.0)

1·2단계(사용자 교정 채널·회상 실패 격차 후보)에 이어, 파이프라인의
남은 케이스 2종(A안 훅 감지·검증된 성공 강화)과 전체 입구의 단일
등록표를 완결한다(원 제안 — docs/plans/2026-11-19-flint-knowledge-pipelines.md·
스펙 docs/plans/archive/2026-11/2026-11-21-flint-v1140.md).

- **A안 훅 감지 — go 판정(구현)**: v1.12.0 부록의 판정 기준을 합성
  데모 세션으로 실측해 **go**로 확정했다 — 재현율 10/10(교정 프롬프트
  전부 매칭)·오탐 0. `UserPromptSubmit` 훅(hooks.json →
  `flint-plugin/hooks/correction_filter.sh`)이 로컬 필터로 교정 마커
  최소 집합(한국어 "그게 아니라/틀렸어/아니야/다시"·영어
  "actually/no, /that's wrong/correction:")을 매칭하고, 매칭 조각만
  (마커 ± 80 코드포인트) `flint write knowledge --source correction`
  confidence 0.4로 적립한다(conf_floor 미만 — 항상 pending, B안
  기본 0.7과 동일 승격 회로). 훅 계약 테스트는 `make ci` 상시 실행.
- **프라이버시 원칙(전체 발언 캡처 금지 승계)**: stdin에 프롬프트
  전문이 오지만 스크립트가 매칭에 실패하면 flint 호출 자체를 하지
  않는다(DB 쓰기 0)·flint는 매칭 조각만 수신한다(전문 미수신)·
  비매칭·실패 전부 exit 0 무출력(훅 non-blocking — 프롬프트 진행
  불변). 훅 디버그 로그에 전문 잔류 없음.
- **온/오프 스위치**: 환경변수 `FLINT_CORRECTION_HOOK=off` 즉시
  exit 0(파싱·호출 전 차단). 기본 on.
- **survived 완화 — 옵트인**: 관측 정의는 동일 (knowledge_id,
  session)의 재회상 2회 이상(knowledge_reads 파생 — 종류 불문 read·
  hot 전부 카운트, `SurvivedCounts` 일괄 조회). 감쇠 유효 점수 산식은
  `count − max(0, rate×days − survived × relief)` — 완화는 감쇠 상쇄만
  가능(내부 클램프·성장 아님), 외부 max(0,·) 바닥 유지. 튜닝 키
  `survived_decay_relief` **기본 0 = 비활성**(발행 시점 동작 불변
  룰링 — 완화는 opt-in, meta 오버라이드로만 켠다). 0이면 survived
  조회 자체를 생략해 출력이 바이트 단위 동일. 소비자: hot·brief
  랭킹·maintain --decay-audit. `:vol:immutable:` 면제가 우선(면제행은
  감쇠 자체가 없으므로 survived 무의미).
- **케이스 등록표 8종(단일 원천)**: 입구 케이스 전부가 Go 등록표
  (lib/knowledge/pipeline.go)에 존재하고 아래 매트릭스는 그 파생
  렌더링이다(문서-코드 어긋남은 계약 테스트가 차단). source 보유
  케이스는 sourceRegistry와 정합. v1.17.0 에코 케이스 추가(관측·
  판정 지원 — source 없음).

| 케이스 | 입구 신호 | source | 태그 | confidence | 구현 |
| --- | --- | --- | --- | --- | --- |
| 교정 B안(에이전트 계약) | 사용자 교정을 에이전트가 인지해 즉시 적립(스킬 지침) | correction | correction | 0.7 | v1.11.0 |
| 교정 A안(훅 감지) | UserPromptSubmit 훅 — 로컬 필터가 교정 마커 매칭 | correction | correction | 0.4 | v1.14.0 |
| 회상 실패·반복 | owned_miss 당일 누적이 gap_query_threshold 도달 | gap | gap-candidate | 0.4 | v1.12.0 |
| MCP 도구 호출 | flint MCP 서버 경로 관측 | mcp | — | 관측 | v0.2.6 |
| 세션 증류 | PostCompact 훅 — 압축 요약의 세션 행 적립 | hook | — | 0.5 | v0.27 |
| 수동 | 사람 명시 적립(confidence·태그 지정) | manual | — | 지정 | v0.1 |
| 검증된 성공 | 동일 (knowledge_id, session) 재회상 관측 | — | — | 관측 | v1.14.0 |
| 에코 검토 | write 응답 echo 필드 — 근접 활성 행 duplicates/contradicts 보고 | — | — | 관측 | v1.17.0 |

## 지식화 파이프라인 4단계 — 에코(write 시점 모순 감지)·관측 키 (v1.17.0)

(spec — docs/plans/2026-11-21-flint-v1170.md). 쓰기 시점 자기검토:
새 적립이 기존 활성 지식과 충돌하는지를 쓰기 응답에 보고한다.

- **개념**: `flint write knowledge`가 새 행을 커밋할 때, dedup 통과
  직후·분류 전에 활성 지식(invalid_at IS NULL, suggest_scan_cap 상한)
  을 스캔해 입력(topic+summary, 4096 룬 상한)과 근접한 행을 찾는다
  (`EchoScan` — bigramDice·BigramDiceMultiset 재조합, 신규 유사도
  없음). 정확-hash 중복은 이미 DuplicateMeta 응답이 존재하므로
  에코를 생략한다(중복 보고 방지).
- **echo 필드 해석**: 단일 행 커밋 응답에
  `"echo":[{id,topic,score,relation}]`가 추가된다(기존 필드 불변·
  하위 호환). `relation` 두 값 — `duplicates`: multiset 유사도 ≥
  conflict_dup_threshold(0.6, 기존 판정 승계) = 같은 내용 재적립
  후보. `contradicts`: 0.6 미만이면서 집합 유사도 ≥
  echo_min_score(0.3)·토픽 유사도 ≥ echo_topic_match(0.4) = 같은
  주제의 상이 주장. `score`는 multiset 유사도(정렬 척도·duplicates는
  ≥0.6 보장). 상위 echo_top_k(기본 5)건·미달 행은 제외, 스캔 실패 시
  필드 부재·쓰기는 정상 완료(§13).
- **판정 가이드**: 에코는 제안만 — **판정은 에이전트**가 기존 승인
  회로로 수행한다(자동 supersede 금지·§5 승계). duplicates → 기존
  행으로 회상(read) 전환 또는 새 행 철회(재적립 불필요).
  contradicts → 어느 쪽이 최신인지 맥락으로 판정해 저신뢰 행
  적립 후 `approve --id N --supersede OLD`, 또는 기존 행
  `maintain --supersede OLD --by NEW`·`--invalidate`. 저신뢰(기본
  0.4) pending 행은 승격 게이트를 거친다.
- **관측 키 3종(소스별 전환 카운터)**: `correction_approved`
  (origin=correction 승격)·`gap_approved`(origin=gap 승격)·
  `survived_sessions`(아래). 승격 경로(approve)에서 소스별로
  카운트된다(일일 meta 플랫 키, PruneDaily 정리 — 관측이 판정을
  대신하지 않는다).
- **maintain survived 스냅숏**: `flint maintain --decay-audit` 실행 시
  랭킹 시점 SurvivedCounts 결과에서 survived≥1인 id 수를 일일
  `survived_sessions` 카운터로 스냅숏한다(완화 비활성
  survived_decay_relief=0이면 조회 자체를 생략 — 스냅숏도 no-op).
  관측 상실은 조용(§13).

## MCP 서버 — structuredContent·outputSchema·버전 협상 (v1.18.0)

(spec — docs/plans/2026-11-21-flint-v1180.md). `flint mcp` stdio 서버의
프로토콜 계약. 최신 클라이언트는 구조화 소비·스키마 검증이 가능하고,
구 클라이언트는 기존 content 텍스트 경로 그대로(하위 호환).

- **버전 협상**: 지원 집합 {2024-11-05, 2025-06-18, 2025-11-25} —
  initialize의 protocolVersion이 집합 내면 동일 버전 응답(echo),
  밖이면 서버 최신(2025-11-25)으로 응답(사양 MUST). absent·비문자열은
  기존 fallback 2024-11-05 유지(별도 규정).
- **outputSchema**: tools/list의 5개 도구가 실출력 기준 출력 스키마를
  함께 선언한다 — write{status,hash,state,id}·approve{status,id}·
  pending{status,items,counts,next}·read{type:array, items:{id,topic,
  summary}}(JSONL 행 배열)·hot{type:object} 최소 메타(비JSON 텍스트).
- **structuredContent**: tools/call 응답은 content 텍스트를 유지한 채,
  출력이 단일 JSON 객체면 그 객체를 structuredContent로 추가하고
  (write·approve·pending 응답, read zero-hit {"hits":0})·JSONL 다중
  행이면 행별 파싱→{results:[...]} 래핑. 비JSON(hot)·파싱 실패·
  배열/스칼라는 생략. isError 매핑은 v1.17 이전과 불변(exit 3·exit 1+
  출력 → true, zero-hit exit 1+빈 출력 → false).
- **read 구조화**: flint_read는 내부적으로 `--json`을 조립한다 — MCP
  경로의 read는 항상 기계 판독 JSONL 행(content 텍스트는 원문 유지),
  1행도 {results:[행]} 래핑, {error:...}행은 그대로 노출. 클라이언트는
  structuredContent.results 또는 content 텍스트 중 편한 쪽을 소비한다.

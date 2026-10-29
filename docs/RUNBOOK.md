# RUNBOOK — flint 운영 절차

감사·오염 대응·복구 훈련·백업 보존·진단·스테일 관리·유지보수 주기의
8절 구성. 모든 절차는 각 커맨드 계약(exit 0/1/2/3, 한 줄 JSON)을 따른다.

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

- `flint restore --drill <file>` — 덤프 파일을 임시 스키마 `flint_drill`에
  재생하고 행수를 대조한다. 결과는 `maintenance_runs`에 기록되고
  `meta.last_drill`이 갱신된다.
- `flint maintain --check`가 30일 경과를 보고한다. 경과 시 최신 덤프로
  drill을 반복한다. 주기: **30일마다 1회**.

## 4. 백업 보존 — keep-N

- `flint backup` — `meta.backup_keep`(기본 14)에 따라 오래된 덤프와
  sha256 사이드카를 함께 삭제한다(keep-N).
- 보존 수는 `meta` 테이블의 `backup_keep` 값으로 조정한다(기본 14).

## 5. 진단 — `flint doctor` (v0.6)

검사 5종(brew doctor 준용 — `--list-checks`로 목록, `--check NAME`으로
서브셋 실행, 무효 검사명은 exit 3):

| 검사 | 내용 | fail/warn |
|---|---|---|
| hook-file | Claude 설정·플러그인 캐시에 flint 훅 등록 여부 (`FLINT_CLAUDE_HOME` 오버라이드 지원) | fail |
| db | DB Ping 접속 | fail |
| schema | meta schema_version ≥ 4 일치(v0.8 현재 5) | fail |
| integrity | `flint_knowledge_*` 인덱스 3행 존재 | fail |
| last-seen | 3경로(write/hot/read) last-seen 경과 + grace(stall_hot_h 48h·stall_write_h 72h) | warn |

- **fail만 exit 1**, warn은 exit 0. fail마다 수정 명령 한 줄이 함께 출력된다.
- 경고(stall)는 `stall_hot_h`·`stall_write_h` meta 키로 조정(TUNING.md §2).

fail 시 대응:

1. **hook-file** — 출력된 fix 문구의 안내 설치 커맨드를 실행한다
   (플러그인 설치·`FLINT_CLAUDE_HOME` 경로 확인). 훅 없이도 flint CLI는
   동작하므로 세션은 즉시 복구 필요 없음.
2. **db** — `docker compose up -d`로 Doltgres 기동 후 재실행.
   DSN은 `FLINT_DSN` 확인(기본 `localhost:5434`).
3. **schema** — `flint setup`(마이그레이션) 실행 후 `flint doctor --check schema`
   로 재확인.
4. **integrity** — 인덱스 재생성 필요. 최신 백업 덤프가 있으면 §4 절차로
   복구 검토, 없으면 `flint backup` 후 대응.
5. **last-seen(warn)** — hot 48시간·write 72시간 부재. 세션을 시작해
   `flint hot`/`flint write`를 한 번 실행하면 해소된다(지속되면 훅 등록
   상태를 hook-file 검사로 점검).

## 6. 스테일 관리 — `flint maintain --stale` (v0.6 → v0.8 확장)

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

cron 예(매월 1일 04:00, dry-run만):

```
0 4 1 * * FLINT_DSN=postgresql://flint:flint-dev@localhost:5434/flint \
  /usr/local/bin/flint maintain --check >> "$HOME/.flint/maintain.log" 2>&1
```

launchd 예(`~/Library/LaunchAgents/dev.flint.maintain.plist`):

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>dev.flint.maintain</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/flint</string>
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

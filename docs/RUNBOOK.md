# RUNBOOK — flint 운영 절차

감사·오염 대응·복구 훈련·백업 보존·진단·스테일 관리의 6절 구성. 모든
절차는 각 커맨드 계약(exit 0/1/2/3, 한 줄 JSON)을 따른다.

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
| schema | meta schema_version = 4 일치 | fail |
| integrity | `flint_knowledge_*` 인덱스 3행 존재 | fail |
| last-seen | 3경로(write/hot/read) last-seen 경과 + grace(stall_hot_h 48h·stall_write_h 72h) | warn |

- **fail만 exit 1**, warn은 exit 0. fail마다 수정 명령 한 줄이 함께 출력된다.
- 경고(stall)는 `stall_hot_h`·`stall_write_h` meta 키로 조정(TUNING.md §2).

fail 시 대응:

1. **hook-file** — 출력된 fix 문구의 안내 설치 커맨드를 실행한다
   (플러그인 설치·`FLINT_CLAUDE_HOME` 경로 확인). 훅 없이도 flint CLI는
   동작하므로 세션은 즉시 복구 필요 없음.
2. **db** — `docker compose up -d`로 Doltgres 기동 후 재실행.
   DSN은 `FLINT_DSN` 확인(기본 `localhost:5433`).
3. **schema** — `flint setup`(마이그레이션) 실행 후 `flint doctor --check schema`
   로 재확인.
4. **integrity** — 인덱스 재생성 필요. 최신 백업 덤프가 있으면 §4 절차로
   복구 검토, 없으면 `flint backup` 후 대응.
5. **last-seen(warn)** — hot 48시간·write 72시간 부재. 세션을 시작해
   `flint hot`/`flint write`를 한 번 실행하면 해소된다(지속되면 훅 등록
   상태를 hook-file 검사로 점검).

## 6. 스테일 관리 — `flint maintain --stale` (v0.6)

- `flint maintain --stale [--days N] [--limit M]` — active 항목 중
  (last_read_at < now-N일) 또는 (read_count=0 AND ingested_at < now-N일)
  항목을 보고한다. `--days` 기본은 meta `stale_days`(기본 90),
  `--limit` 기본 20.
- 미참조 버킷(read_count=0)은 never_read로 구분 표시한다.
- **보고 전용·exit 0** — 물리 삭제·자동 confidence 변경 없음. 처리는
  기존 `maintain --invalidate`(무효화=가역)로 운영자 판단 후 수행.
- 주기: 분기 리뷰(90일 관례)에 맞춰 cron·수석 회고 시 실행. 결과의
  `days90`·`never_read` 수치는 `flint report`의 stale 섹션과 상호 확인.

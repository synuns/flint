# RUNBOOK — flint 운영 절차

감사·오염 대응·복구 훈련·백업 보존의 4절 구성. 모든 절차는 각 커맨드
계약(exit 0/1/2/3, 한 줄 JSON)을 따른다.

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

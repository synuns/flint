# backup·drill — 덤프·사이드카·복구 훈련

backup은 flint DB 전체를 자체 SQL 덤프 스크립트로 내보내고, restore는
그 덤프를 재생한다. `restore --drill`은 재생을 임시 데이터베이스
`flint_drill_db`에서 훈련해 행 수가 원본과 일치하는지만 보고한다 —
원본 DB는 불변이다. 사용자가 얻는 것은 덤프 파일, `.sha256` 사이드카,
드릴 성공 보고 한 줄이다.

## Sub-features

- `backup-stdout` — 무인자·`--out -`은 덤프를 stdout에 쓴다(평문 경고
  stderr 1행).
- `backup-file` — `--out FILE`은 0600 퍼모드 파일 + `.sha256` 사이드카를
  만들고 디렉터리 로테이션(기본 14 유지)을 돈다.
- `backup-encrypt` — `--encrypt` age 암호화. `--out` 필수.
- `backup-mask` — `--mask` 비밀 패턴 치환 진단 덤프. 비복원 아티팩트.
- `restore-guard` — 재생 전 체크섬 검증. 불일치·사이드카 부재는 재생
  자체를 차단한다.
- `restore-drill` — `--drill` 재생 훈련. 결과는
  `{"status":"drill","expected":N,"restored":N,"ok":bool}`.

## How to get to it (user POV)

- `flint backup` — 덤프를 stdout으로.
- `flint backup --out <file> [--encrypt] [--mask]` — 파일로.
- `flint restore --file <file> [--drill]` — 재생(훈련).

## Driving it with Claude Code

Preconditions:

- 검증 DSN `postgresql://flint:flint-dev@localhost:5434/flint_verify`에
  빌드·시드가 끝난 상태(스킬 Launch 참조).
- 덤프는 repo 밖 `/tmp/flint-verify/`에 쓴다(git 경고는 8번 여정에서
  의도적으로 시험).
- 증거는 `.superlite/verify/<version>/captures/`에, 판정은 manifest에.

- **파일 백업.** `mkdir -p /tmp/flint-verify && FLINT_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify' ./bin/flint backup --out /tmp/flint-verify/dump.sql`. Exit 0, stdout 1행 `{"status":"ok","path":"/tmp/flint-verify/dump.sql","sha256":"<64자>"}`. `ls -l /tmp/flint-verify/`가 `-rw-------`(0600) 덤프와 `dump.sql.sha256`를 보인다. `head -1 dump.sql`은 `-- flint backup (ADR-0002) generated`로 시작한다.
- **stdout 평문 경고.** `FLINT_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify' ./bin/flint backup > /tmp/flint-verify/stdout.sql 2> /tmp/flint-verify/stdout.err`. Exit 0. stderr에 `[flint] backup: writing plaintext dump to stdout — beware terminal and log leaks (saving to a file is recommended)` 1행, stdout은 경고 없는 SQL 본문. `--out -`이 같은 stdout 모드다.
- **드릴 훈련.** `FLINT_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify' ./bin/flint restore --file /tmp/flint-verify/dump.sql --drill`. Exit 0, `{"status":"drill","expected":N,"restored":N,"ok":true}` — N은 knowledge 행 수.
- **드릴 부작용 관측.** 드릴 직후 `flint status`의 pending 수는 드릴 전과 동일(원본 불변). `docker compose exec doltgres psql -U flint -d postgres -tAc "SELECT count(*) FROM pg_database WHERE datname='flint_drill_db'"`가 0(임시 DB 항상 청거). `docker compose exec doltgres psql -U flint -d flint_verify -tAc "SELECT value FROM meta WHERE key='last_drill'"`이 RFC3339 시각(성공 기록).
- **무결성 가드.** 사이드카 한 글자를 바꾸고 `restore --file dump.sql --drill` → exit 1, `{"error":"checksum_mismatch","message":"backup files run arbitrary SQL — checksum tampering detected","path":...}`. 사이드카를 지우고 재시도 → exit 1, `{"error":"checksum sidecar missing — blocking replay without integrity verification (regenerate the backup: flint backup --out)"}`. 두 경우 모두 재생은 일어나지 않는다.
- **마스크 덤프.** `FLINT_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify' ./bin/flint backup --out /tmp/flint-verify/masked.sql --mask`. Exit 0, `"masked":true`가 붙고 `sha256` 필드는 없다(사이드카 미부여). `head -1 masked.sql`이 `-- flint-masked-dump: never restore (masked diagnostic artifact)`. `restore --file masked.sql --drill` → exit 1, `{"error":"masked dump — masked dumps are for diagnostics and sharing only (not restorable)"}`.
- **암호화 백업과 복호 드릴.** `mkdir -p /tmp/flint-verify-home && FLINT_HOME=/tmp/flint-verify-home FLINT_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify' ./bin/flint backup --out /tmp/flint-verify/enc.sql --encrypt`. Exit 0, `"encrypted":true`. `/tmp/flint-verify-home/backup.key`(0600)가 생성된다. 같은 `FLINT_HOME`으로 `restore --file enc.sql --drill` → `ok:true`(검증 후 복호해 재생).
- **git 저장소 경고.** `FLINT_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify' ./bin/flint backup --out /tmp/inside-repo-check.sql`를 워크트리 안 경로로 실행하면 stderr에 `[flint] backup: --out path is inside a git repo — risk of committing or syncing the dump. Prefer ~/.flint/backup` 1행, exit 0(진행 유지). 증거용 임시 덤프는 캡처 후 삭제한다.

## Gotchas

- `--drill` 없는 restore는 대상 DB의 행을 DELETE 후 재삽입하는 재생
  스크립트를 트랜잭션으로 돈다(lib/backup/dump.go — `BEGIN; DELETE`) —
  검증 DSN에서도 실재생은 돌리지 않는다.
- 오류 한 줄 `{"error":...}`는 **stdout**으로 나온다. `[flint] backup:`
  경고만 stderr — 캡처할 때 채널을 분리한다.
- `--encrypt` 단독은 exit 3(ciphertext is file-only). meta
  `backup_encrypt=true`면 플래그 없는 `--out` 백업도 자동 암호화된다
  (stdout 파이프는 평문 유지) — 검증 DB는 미설정이라 평문이다.
- 암호화 덤프의 사이드카는 암호문 기준 체크섬이다. 마스킹 덤프는
  사이드카 자체가 없다(restore 원천 차단).
- 드릴은 성공 시 원본 DSN에 maintenance_runs 1행(kind=drill)과 meta
  `last_drill`을 남긴다(lib/backup/drill.go) — 완전 무부작용이 아니다.
- doltgres의 테이블 개별 DROP 무음 no-op 결함과 무관하게 드릴의 DB
  단위 DROP은 동작하고, 잔존하면 `still present — manual DROP required`
  실패로 드러난다(lib/backup/drill.go 실측 주석).
- 드릴 재생 상한 120초·정리 DROP 30초 — 대형 덤프에서 초과하면 실패로
  보고된다.
- backup.key는 FLINT_HOME 밑 `backup.key` — 운영 키와 섞이지 않게
  검증은 FLINT_HOME 임시 디렉터리로 격리한다(7번 여정).

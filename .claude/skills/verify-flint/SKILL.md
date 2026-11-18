---
name: verify-flint
description: flint CLI·TUI를 실사용자 경로로 구동해 동작을 증명하는 검증 스킬. 릴리스(G3)·회귀 의심·기능 변경 직후에 reach for it. 합성 시드 DB만 사용(개인 운영 DB 금지).
---

# verify-flint — flint 실동작 검증

flint는 Go CLI(+bubbletea TUI, MCP). 이 스킬은 바이너리를 실제 구동해
기능별 여정을 돌고 증거를 남긴다. 대상 독자: 이 앱을 본 적 없는 에이전트.

## Launch

짧은 수명 CLI/TUI — 서버를 띄우지 않는다. 준비 = 빌드 1회 + 검증 전용 DB.

```bash
make build                                # → ./bin/flint
make up                                   # doltgres 127.0.0.1:5434 (기동 확인: docker compose ps)
CID=$(docker ps --format '{{.Names}}\t{{.Ports}}' | grep '127.0.0.1:5434' | cut -f1) && docker exec -e PGPASSWORD=flint-dev "$CID" psql -h localhost -U flint -d flint -c "CREATE DATABASE flint_verify" 2>/dev/null || true
FLINT_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify' ./bin/flint status   # 최초 접속이 스키마를 마이그레이션
make seed FLINT_SEED_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify'       # 합성 시드(data/seed.jsonl)
```

준비 신호: `flint status`가 JSON을 내고 exit 0. 준비 안 됨(컨테이너 down·
DSN 오류)이면 드라이브하지 않는다. `FLINT_SEED_DSN` 미지정 시 make seed가
exit 2로 거부하는 것은 의도된 가드(운영 DB 오염 방지) — 항상 검증 DSN을
명시한다. **`FLINT_DSN`(운영) 환경변수가 세션에 남아 있으면 드라이브 전에
해제한다. 모든 드라이브는 FLINT_DSN=검증 DSN을 명시적으로 덮어쓴다.**

Teardown: `make down`은 하지 않는다(다른 작업이 컨테이너를 공유할 수
있다). 검증이 만든 것은 flint_verify DB뿐 — Cleanup 참조.

## Doctor

드라이브 전 1회, read-only:

```bash
FLINT_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify' ./bin/flint doctor
```

`{"status":"ok"}`·exit 0이 아니면 이 인스턴스는 구동 가치 없음 — 원인을
manifest에 기록하고 드라이브 중단. 무언가 이상해 보일 때마다 다시 돌린다.
포트 검사는 cwd의 docker compose 프로젝트를 보므로 워크트리에서 전체
doctor는 실패할 수 있다 — 워크트리에서는 `--check db` 서브셋, 전체
검사는 메인 체크아웃에서.

## Drive

- CLI: `FLINT_DSN=<검증 DSN> ./bin/flint <cmd> [args]` — exit code와
  stdout JSON이 계약(PLAN §7). 오류 라인 `{"error":...}`도 **stdout**으로
  나간다(stderr는 요약·경고 채널).
- TUI: `scripts/verify/tui_capture.sh`(Helpers)로 tmux 세션에서 구동·
  키 시퀀스 주입·텍스트 스냅샷 캡처. 화면 좌표가 아닌 라벨·프롬프트
  문자열을 핸들로 쓴다.
- 여정·기대·주의점은 `features/` 맵이 원천 — 맵 파일 하나가 1드라이브다.

## Evidence

`.superlite/verify/<version>/` (repo 비커밋. `<version>` = `./bin/flint
version` 출력). 구조:

```
manifest.md      # 기능별: 드라이브 명령·판정(pass/fail/blocked)·요약·캡처 목록
captures/        # tmux 텍스트 스냅샷·CLI 출력 원문(기능명-타임스탬프)
```

증명 기준: 내부 setter·테스트 전용 경로가 아니라 실사용자 경로를 돈다.
행동과 결과 상태 둘 다 남긴다 — 출력뿐 아니라 `flint status`·`flint audit`
로 부작용(행 수·커밋)을 확인해 manifest에 적는다. dry-run·test 모드가
있다면 이름을 믿지 말고 관측으로 무엇을 건너뛰는지 확인한다. 실패 여정을
통과로 바꾸지 않는다 — 이유를 Gotchas에 남긴다.

## Cleanup

- 검증이 시작한 tmux 세션만 종료한다(프로세스명 킬 금지 — 세션 단위).
- flint_verify DB는 유지(다음 검증 재사용 — 재시드가 멱등). 증거
  디렉터리는 절대 지우지 않는다. cleanup 후 증거 생존을 확인한다.
- 컨테이너·볼륨은 검증이 소유하지 않았으므로 손대지 않는다.

## Helpers

- `scripts/verify/tui_capture.sh —cmd '<flint TUI 커맨드>' —keys 'tab,enter' —out captures/<이름>.txt`
  tmux 세션 기동→커맨드 구동→키 주입→capture-pane 스냅샷→세션 종료.
  스크립트 헤더에 사용법 전문이 있다.

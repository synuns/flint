# flint 빠른 시작

2단계로 로컬 flint를 띄운다. 각 단계는 1분 이내.

## 1. 설치

```sh
git clone https://github.com/synuns/flint.git flint && cd flint
make install    # 빌드 후 GOPATH/bin/flint에 설치 (Go 1.26+, 의존 pgx/v5·x/text)
```

[릴리스 바이너리](https://github.com/synuns/flint/releases)를 받아 PATH에
둬도 된다 — 바이너리·체크섬·플러그인 zip이 자동 첨부된다.

## 2. 원커맨드 설정

```sh
flint setup     # ①compose up --wait(60s) ②migrate ③seed 안내 ④플러그인 안내
# 예시 데이터: 프로젝트 3 + 결정 5 + pending 1 + 선호 1 + 플레이북 2
make seed FLINT_SEED_DSN=postgresql://flint:flint-dev@localhost:5434/flint
```

setup은 Doltgres를 띄우고(호스트 5434, ADR-0010) 스키마를 마이그레이션한
뒤 단계별 진행을 출력한다(터미널에서는 시각 진행, `--json` 또는 비TTY에서는
한 줄 JSON — v0.17.0). v0.7부터 compose 대기는
`up -d --wait --wait-timeout 60` — 컨테이너 헬스체크 통과까지 최대 60초
기다린 뒤 다음 단계로 진행한다. docker 미설치·compose 실패는 즉시 실패가
아니라 경고로 남고 설치 안내(`https://docs.docker.com/get-docker/`)를 한 줄
출력한 뒤 계속 진행한다 — DB가 이미 기동 상태면 이후 단계(migrate 등)까지
정상 완료된다. DSN 기본값
`postgresql://flint:flint-dev@localhost:5434/flint` — 환경변수 `FLINT_DSN` 또는
설정 파일(아래 4절)로 덮어쓴다. 해제는 `flint teardown`(`--purge` 시
volume까지).

시딩은 재실행해도 안전하다 — 동일 payload는 해시 dedup로
`{"status":"duplicate"}` exit 0 (멱등). 대상 DSN이 명시적인 것은 의도다
(운영 DB 오염 방지 가드). 확인:

```sh
flint read 덤프        # 검색: active_knowledge 뷰만
flint hot --session x  # 세션 시작 시 주입 (hot_budget 기본 2,000자)
flint audit --limit 5  # 감사: dolt_log 최근 5건
flint doctor --check db   # 진단: 검사 16종 중 db 서브셋(fail만 exit 1)
flint report --days 7     # 리포트: 일별 유입·zero_pct·p95·stale (기본 7일)
flint restore --file <dump> --drill  # 복구 훈련(원본 불변, 행수 대조만)
```

## 3. 하네스 연결

**Claude Code** — 플러그인(훅 3종 + `/flint` 커맨드):

```text
/plugin marketplace add synuns/flint
/plugin install flint@flint
```

**Codex·Gemini·Cursor·OpenCode·Copilot** — 자동 등록:

```sh
flint setup --mcp
```

설치된 하네스를 감지해 각자의 MCP 설정에 등록한다. Copilot 자동
등록이 불가한 환경은 수동 절차([adapters/copilot.md](adapters/copilot.md)),
OpenClaw·Hermes는 문서 어댑터([adapters/](adapters/)).

## 4. 설정 파일 재배치(선택)

DSN을 매번 환경변수로 넘기지 않으려면 설정 파일에 둔다(v0.7).

```sh
# Linux: ~/.config/flint/config.json · macOS: ~/Library/Application Support/flint/config.json
mkdir -p ~/.config/flint        # macOS는 mkdir -p "$HOME/Library/Application Support/flint"
printf '{"dsn":"postgresql://flint:flint-dev@localhost:5434/flint"}' \
  > ~/.config/flint/config.json
```

- 기본 위치는 `UserConfigDir()/flint/config.json`(macOS
  `~/Library/Application Support/flint/config.json`,
  Linux `~/.config/flint/config.json`).
- 디렉터리 재배치는 환경변수 `FLINT_CONFIG_DIR`(gh `GH_CONFIG_DIR` 모델).
- 우선순위: `FLINT_DSN` env > `config.json`의 `dsn` > 기본값
  `localhost:5434`. 파일 파손·빈값은 기본값으로 폴백(에러 아님).
- 상세는 [docs/TUNING.md](TUNING.md) §2.

감사·오염 대응·복구 훈련·백업 보존 절차는 [docs/RUNBOOK.md](RUNBOOK.md).

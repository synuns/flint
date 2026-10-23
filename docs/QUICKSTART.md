# flint 빠른 시작

2단계로 로컬 flint를 띄운다. 각 단계는 1분 이내.

## 1. 설치

```sh
git clone https://github.com/synuns/flint.git flint && cd flint
make install    # bin/flint 빌드 (Go 1.25+, 의존 pgx/v5만)
```

## 2. 원커맨드 설정

```sh
./bin/flint setup   # ①compose up --wait(60s) ②migrate ③seed 안내 ④플러그인 안내
make seed           # 예시 데이터: 프로젝트 3 + 결정 5 + pending 1 + 선호 1 + 플레이북 2
```

setup은 Doltgres를 띄우고(호스트 5433, ADR-0001) 스키마를 마이그레이션한 뒤
진행 요약 한 줄 JSON을 출력한다. v0.7부터 compose 대기는
`up -d --wait --wait-timeout 60` — 컨테이너 헬스체크 통과까지 최대 60초
기다린 뒤 다음 단계로 진행한다. docker 미설치 시 즉시 실패하며 설치 안내
(`https://docs.docker.com/get-docker/`)를 한 줄 출력한다. compose·ping
실패는 경고로 남고 다음 단계 안내는 유지된다. DSN 기본값
`postgresql://flint:flint-dev@localhost:5433/flint` — 환경변수 `FLINT_DSN` 또는
설정 파일(아래 3절)로 덮어쓴다. 해제는 `./bin/flint teardown`(`--purge` 시
volume까지).

시딩은 재실행해도 안전하다 — 동일 payload는 해시 dedup로
`{"status":"duplicate"}` exit 0 (멱등). 확인:

```sh
./bin/flint read 덤프        # 검색: active_knowledge 뷰만
./bin/flint hot --session x  # 세션 시작 시 주입 (hot_budget 기본 2,000자)
./bin/flint audit --limit 5  # 감사: dolt_log 최근 5건
./bin/flint doctor --check db   # 진단: 검사 5종 중 db 서브셋(fail만 exit 1)
./bin/flint report --days 7     # 리포트: 일별 유입·zero_pct·p95·stale (기본 7일)
./bin/flint restore --file <dump> --drill  # 복구 훈련(원본 불변, 행수 대조만)
```

## 3. 설정 파일 재배치(선택)

DSN을 매번 환경변수로 넘기지 않으려면 설정 파일에 둔다(v0.7).

```sh
mkdir -p ~/.config/flint
printf '{"dsn":"postgresql://flint:flint-dev@localhost:5433/flint"}' \
  > ~/.config/flint/config.json
```

- 기본 위치는 `UserConfigDir()/flint/config.json`(macOS
  `~/Library/Application Support/flint`, Linux `~/.config/flint`).
- 디렉터리 재배치는 환경변수 `FLINT_CONFIG_DIR`(gh `GH_CONFIG_DIR` 모델).
- 우선순위: `FLINT_DSN` env > `config.json`의 `dsn` > 기본값
  `localhost:5433`. 파일 파손·빈값은 기본값으로 폴백(에러 아님).
- 상세는 [docs/TUNING.md](TUNING.md) §2.

감사·오염 대응·복구 훈련·백업 보존 절차는 [docs/RUNBOOK.md](RUNBOOK.md).

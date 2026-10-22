# flint 빠른 시작

2단계로 로컬 flint를 띄운다. 각 단계는 1분 이내.

## 1. 설치

```sh
git clone <repo-url> flint && cd flint
make install    # bin/flint 빌드 (Go 1.25+, 의존 pgx/v5만)
```

## 2. 원커맨드 설정

```sh
./bin/flint setup   # ①docker compose up -d ②migrate ③seed 안내 ④플러그인 안내
make seed           # 예시 데이터: 프로젝트 3 + 결정 5 + pending 1 + 선호 1 + 플레이북 2
```

setup은 Doltgres를 띄우고(호스트 5433, ADR-0001) 스키마를 마이그레이션한 뒤
진행 요약 한 줄 JSON을 출력한다. compose·ping 실패는 경고로 남고 다음 단계
안내는 유지된다. DSN 기본값
`postgresql://flint:flint-dev@localhost:5433/flint` — 환경변수 `FLINT_DSN`으로
덮어쓴다. 해제는 `./bin/flint teardown`(`--purge` 시 volume까지).

시딩은 재실행해도 안전하다 — 동일 payload는 해시 dedup로
`{"status":"duplicate"}` exit 0 (멱등). 확인:

```sh
./bin/flint read 덤프        # 검색: active_knowledge 뷰만
./bin/flint hot --session x  # 세션 시작 시 주입 (hot_budget 기본 2,000자)
./bin/flint audit --limit 5  # 감사: dolt_log 최근 5건
./bin/flint restore --file <dump> --drill  # 복구 훈련(원본 불변, 행수 대조만)
```

감사·오염 대응·복구 훈련·백업 보존 절차는 [docs/RUNBOOK.md](RUNBOOK.md).

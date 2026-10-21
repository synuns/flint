# flint 빠른 시작

3단계로 로컬 flint를 띄운다. 각 단계는 1분 이내.

## 1. 설치

```sh
git clone <repo-url> flint && cd flint
make install    # bin/flint 빌드 (Go 1.23+, 의존 pgx/v5만)
```

## 2. 저장소 기동 (flint setup 자리)

```sh
make up                     # docker compose up -d — Doltgres, 호스트 5433 (ADR-0001)
./bin/flint status          # {"status":"ok","schema_version":"1","pending":N} 확인
```

DSN 기본값 `postgresql://flint:flint-dev@localhost:5433/flint` — 환경변수
`FLINT_DSN`으로 덮어쓴다.

## 3. 최초 시딩

```sh
make seed      # 활성 프로젝트 3건 + flint 기획 결정 5건 + pending 1건 + 사용자 선호
```

재실행해도 안전하다 — 동일 payload는 해시 dedup로 `{"status":"duplicate"}`
exit 0 (멱등). 확인:

```sh
./bin/flint read 덤프        # 검색: active_knowledge 뷰만
./bin/flint hot --session x  # 세션 시작 시 주입 (상한 2,000자)
```

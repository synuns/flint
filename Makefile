DSN ?= postgresql://flint:flint-dev@localhost:5433/flint

.PHONY: test tidy up down build install seed e2e

test:
	go test ./...

tidy:
	go mod tidy

# Doltgres (ADR-0001: 호스트 5433 -> 컨테이너 5432)
up:
	docker compose up -d

down:
	docker compose down

build:
	go build -o bin/flint ./cmd/flint

# QUICKSTART 1단계: 설치 = 빌드(bin/flint 하나면 끝)
install: build

# 시딩: seed.jsonl을 kind별로 나눠 flint write에 파이프.
# 2회째는 전부 duplicate·exit 0(멱등 — 해시는 valid_at·ts 제외 경로).
seed: build
	@for kind in project knowledge; do \
	  grep '"kind":"'"$$kind"'"' data/seed.jsonl | FLINT_DSN='$(DSN)' ./bin/flint write $$kind || exit 1; \
	done

# e2e(T11): 실 Doltgres 필수, 빌드 태그로 분리
e2e:
	go test -tags e2e ./tests/e2e/

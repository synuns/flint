DSN ?= postgresql://flint:flint-dev@localhost:5434/flint

# v0.9.3 R1 테스트 격리: tests/는 이 DSN만 쓴다(기본 flint_test — 운영
# flint와 분리). test·e2e 타깃에만 주입하고 DSN(운영) 기본값은 그대로.
FLINT_TEST_DSN ?= postgresql://flint:flint-dev@localhost:5434/flint_test

# 버전 주입(T33): 기본 dev, 릴리스는 make build VERSION=0.3.0
# v0.12.0 R2: 최근 git 태그에서 파생, 태그 부재 클론은 dev 폴백.
VERSION ?= $(shell git describe --tags --abbrev=0 2>/dev/null || echo dev)
# VERSION_NU — 태그명의 v 접두 제거(release 타깃 안내용. 파생값 v0.9.2 →
# 0.9.2; dev처럼 v 없는 값은 불변).
VERSION_NU := $(patsubst v%,%,$(VERSION))
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
# NEXT_TAG — 릴리스 안내용 다음 패치 버전(최신 태그의 패치 +1; 태그 부재
# 시 v0.1.0). v0.13.2 N12: 기존 안내가 VERSION_NU(최신 태그)를 그대로
# 지시해 이미 존재하는 태그 생성을 유도하던 문제 수정.
NEXT_TAG ?= $(shell cur=$$(git describe --tags --abbrev=0 2>/dev/null); if [ -n "$$cur" ]; then echo "$$cur" | awk -F. '{print $$1"."$$2"."($$3+1)}'; else echo v0.1.0; fi)
# version은 goreleaser와 동일하게 v 접두 없이 주입(VERSION_NU) —
# `flint version`·goreleaser 빌드가 같은 형식(0.13.0)을 출력한다.
LDFLAGS := -X github.com/synuns/flint/lib/cli.version=$(VERSION_NU) -X github.com/synuns/flint/lib/cli.commit=$(GIT_COMMIT)

.PHONY: test tidy up down build install seed e2e release ci lint docs-sync

test:
	FLINT_TEST_DSN='$(FLINT_TEST_DSN)' go test ./...

# CI(T43): 로컬 검증 = YAML 구조 검증 + 전체 단위 테스트.
# Actions(ci.yml)도 이 타깃 실행 후 make e2e — 로컬과 CI 동일 경로.
# GO_TEST_FLAGS: CI가 -p 1(패키지 직렬화) 주입 — 서비스 Doltgres 1개에
# slow 스위트 3개(db·backup·regression) 병렬이 연결 소진시킴(v0.15.2).
GO_TEST_FLAGS ?=
ci:
	python3 scripts/validate_yaml.py
	python3 scripts/check_personal.py
	@test -z "$$(gofmt -l lib cmd)" || { echo 'gofmt 미적용 파일:'; gofmt -l lib cmd; exit 1; }
	FLINT_TEST_DSN='$(FLINT_TEST_DSN)' go test $(GO_TEST_FLAGS) -count=1 ./...

# v1.2.0(사용자 지시): 배포 전 문서 정합 게이트 — 문서 계약 테스트는
# go test ./docs(계약 테스트 41종: versions·badge·runbook·tuning 등)로
# 이미 포함되나 배포 타깃에서 명시적 선행 단계로 격상·문서=코드와 동일
# 게이트. docs-sync는 README·SKILL·contracts 6종의 코드-문서 동기
# 단얫(커맨드 표==Registered 등)을 별도 타깃으로 노출해 릴리스 체크리스트
# 에서 보이게 한다(v1.2.3 T7: release 타깃이 docs-sync를 선행 의존).
# v1.2.3 T7 실측: -run 정규식에 매칭되는 문서 계약 테스트 41종.
docs-sync:
	go test ./docs -count=1 -run 'TestVersion|TestReadme|TestCommand|TestSkill|TestContracts|TestPlan|TestRunbook|TestTuning|TestPlugin|TestServer|TestMcpPublisher' -v 2>&1 | grep -E "^(=== RUN|--- FAIL|--- PASS)" | tail -50

tidy:
	go mod tidy

# lint — staticcheck 정적 분석(v0.18.3). 버전 고정(재현성 — mcp-publisher
# 고정 관례 준용). PATH 우선, 없으면 GOPATH/bin 폴백 조회.
STATICCHECK_VERSION ?= 2026.2.2
STATICCHECK := $(shell command -v staticcheck 2>/dev/null || echo "$(shell go env GOPATH 2>/dev/null)/bin/staticcheck")
lint:
	@test -x "$(STATICCHECK)" || { echo "staticcheck 미설치 — go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)"; exit 2; }
	"$(STATICCHECK)" ./...

# Doltgres (ADR-0010: 호스트 5434 -> 컨테이너 5432)
# v0.7: --wait로 헬스체크 통과까지 대기 후 반환(60s 타임아웃 — setup과 동일).
up:
	docker compose up -d --wait --wait-timeout 60

down:
	docker compose down

build:
	go build -ldflags "$(LDFLAGS)" -o bin/flint ./cmd/flint

# QUICKSTART 1단계: 설치 = 빌드 후 GOPATH/bin에 복사(README의 bare `flint`
# 호출·플러그인 PATH 요구 충족). ldflags 주입 버전이라 bin/flint를 그대로 쓴다.
install: build
	@dest="$$(go env GOPATH)/bin"; mkdir -p "$$dest" && cp bin/flint "$$dest/flint"
	@echo "설치됨: $$(go env GOPATH)/bin/flint"

# 시딩: seed.jsonl을 kind별로 나눠 flint write에 파이프.
# 2회째는 전부 duplicate·exit 0(멱등 — 해시는 valid_at·ts 제외 경로).
# 데모 시드 — 기본 대상 없음(운영 DB 오염 방지): FLINT_SEED_DSN 필수.
seed: build
	@if [ -z "$(FLINT_SEED_DSN)" ]; then echo "FLINT_SEED_DSN 미지정 — 데모 시드 대상(예: 테스트 DSN)을 명시하세요" >&2; exit 2; fi
	@for kind in project knowledge; do \
	  grep '"kind":"'"$$kind"'"' data/seed.jsonl | FLINT_DSN='$(FLINT_SEED_DSN)' ./bin/flint write $$kind || exit 1; \
	done

# e2e(T11): 실 Doltgres 필수, 빌드 태그로 분리
e2e:
	FLINT_TEST_DSN='$(FLINT_TEST_DSN)' go test -tags e2e ./tests/e2e/

# 릴리스(T33): 빌드 후 태그 push 안내만 — 실제 태그·push는 사용자 확인 후 수행.
# 안내 태그는 NEXT_TAG(최신 태그 +패치) — 이미 존재하는 태그를 지시하지
# 않는다(v0.13.2 N12).
release: docs-sync build
	@echo "릴리스 절차(사용자 확인 후): git tag $(NEXT_TAG) && git push origin $(NEXT_TAG) -> Actions 빌드 -> Release 첨부"
	@echo "현재 빌드: $$(./bin/flint version)"

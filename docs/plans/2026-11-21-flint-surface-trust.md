# flint 표면 신뢰 — 버전 정합·업데이트 공지 신선도·데모 시드 (2026-11-21)

상태: active
원천: docs/PLAN.md §6 이월③(표면·배포 인접) — 2026-11-21 갭 점검에서 누락
확인. 업데이트 공지 결함은 실증됨(구버전을 '새 버전'으로 공지 — 메커니즘은
§0)·플러그인 .mcp.json이 PATH `flint` 사용 확인·setup ③단계가 `make seed`
(소스 체크아웃 전제) 안내.
준수: CLAUDE.md(비간섭·합성 데이터) · LOOP.md(TDD·SOLID·게이트) ·
ADR-0014(릴리스 서명)

> 2026-11-21 기획 리뷰 1차 심사(CHANGES_REQUIRED) 반영 완료 — 모순 검출기
> 경계·스탬프 주체 재설계·--reset 안전장치 보강. 릴리스 편성 미정 — 투두 유지.

## 0. 문제

1. **업데이트 공지 과소보고**: 업데이트 확인은 24h 캐시 1회. 하루 여러
   릴리스가 나오는 캐던스에서 캐시의 "latest"가 시간이 지난 값으로
   굳어, 실제 최신보다 오래된 버전을 '새 버전'으로 공지할 수 있다
   (2026-11-21 실증 관찰).
2. **표면 버전 스큐 무검사**: Claude Code 플러그인(.mcp.json)은 PATH의
   `flint`를 실행한다. 어떤 표면이 어떤 버전을 돌리는지 doctor가 보지
   못한다 — "수정됐는데 안 고쳐지는" 원인이 되는 무음 드리프트.
3. **바이너리 사용자 시드 경로 부재**: `go install`·릴리스 아티팩트로
   설치한 사용자는 소스 체크아웃이 없어 setup의 `make seed` 안내를
   수행할 수 없다 — 첫 사용이 빈 DB에서 시작된다.

## 1. 레퍼런스 조사 (2026-11-21, 웹)

**조건부 요청(캐시 신선도의 공식 해법)**
- GitHub REST 가이드 — 조건부 요청(If-None-Match/ETag)으로 미변경 시
  304 Not Modified.
  https://docs.github.com/rest/guides/best-practices-for-using-the-rest-api
- GitHub 커뮤니티 — 304 응답은 레이트 리밋을 소모하지 않음.
  https://github.com/orgs/community/discussions/156480
- 레이트 리밋 최적화 해설 — ETag 저장·재검증 패턴.
  https://www.jamiemagee.co.uk/blog/making-the-most-of-github-rate-limits/

**데모 데이터 로딩 선례**
- Neo4j `:play movies` — 도구 안에서 샘플 데이터를 불러오는 튜토리얼
  명령의 정형 선례. https://neo4j.com/docs/getting-started/appendix/example-data
  · https://neo4j.com/blog/developer/getting-started-with-play-movies

**내부 자산**
- data/seed.jsonl — 합성 데모 데이터가 이미 존재(kind별 jsonl, 검증
  파이프라인 통과 형식). Go `embed`로 바이너리에 내장 가능.

## 2. 설계 — 채택: 조건부 재검증 3원칙 + 정합 가시화 + 격리 데모

**A. 업데이트 캐시 3원칙**

1. **조건부 재검증** — 캐시에 ETag를 저장, 재검증은 If-None-Match로.
   304는 레이트 리밋 무과금(§1)이므로 확인 빈도 상향의 비용이 0에
   수렴한다.
2. **불가능 상태 즉시 갱신** — 캐시의 latest **<** 현재 실행 버전이면
   모순(구버전이 최신일 수는 없음) → 즉시 재 fetch. 등호(= 최신 실행
   상태)는 정상 — 캐시 갱신만. 재 fetch는 모순 검출·캐시 만료 시에만
   수행하고 네트워크 실패는 조용히 진행(PLAN §4 원칙 3) — 세션 시작
   비간섭 비용을 추가하지 않는다.
3. **공지에 근거 시점 노출** — 공지·status --text에 "확인 시점"을
   붙인다(신선도 자가 진단 — 관측 건전성 O계열과 같은 방향).

**B. doctor 정합 가시화(checkVersions)**

- 자기 버전·캐시 latest(+나이)·PATH flint 경로(exec.LookPath)·
  mcp-registry 발행 버전(동일 조건부 요청)을 나란히 표시.
- **표면 버전 관측은 자가 보고**(리뷰 보강): 플러그인 설치·갱신은
  마켓플레이스가 수행하므로 flint가 그 시점을 포착할 수 없다. 대신
  세션 시작 훅을 실행하는 flint가 **자기 버전을 meta에 자가 기록** —
  실행 시점 계량으로 표면별 최근 실측 버전을 확보하고, doctor는 자가
  보고 vs 자기 버전·캐시 latest를 대조한다.
- 경고는 임계 기반(PLAN §4 원칙 4) — 표시와 경고를 분리.

**C. `flint seed --demo`**

- 합성 seed.jsonl을 `go:embed`로 내장, **일반 write 파이프라인을 통과**(
  검증·content_hash dedup) — 재실행 멱등, 파이프라인 단일 경로 유지.
- **격리 원칙**: 데모 데이터는 개인 운영 DB에 섞이지 않는다. 동일 엔진의
  **별도 데이터베이스**(권장안) 또는 별도 FLINT_HOME — 미결정(§5).
- `flint seed --demo --reset`로 청소(격리 영역 삭제).
- **--reset 안전 장치**(리뷰 보강): 삭제 대상은 **고정 데모 식별자**
  (전용 이름 + 소유 마커 meta)로만 식별 — 이름·마커 불일치 대상은
  거부(exit 3). `--reset --yes` 확인 필수. 비대상 삭제를 원천 차단해
  잘못된 격리 영역 삭제(복구 불가)를 막는다.
- setup ③단계 안내를 `make seed`에서 `flint seed --demo`로 교체 —
  QUICKSTART·setup_ux 문구 동반(문서=코드 동기 게이트).
- 데이터는 전부 합성(개인 데이터 불침투 — CLAUDE.md 최상위 원칙).

**기각 대안(근거 명시)**
- 캐시 TTL 단축만 — 레이트 리밋 소모는 그대로, 과소보고 원인(모순
  상태 검출 부재)을 못 고침.
- 데모 데이터를 실DB에 project=demo로 삽입 — 개인 DB 오염·회상 검색
  오염(top-k 예산 침범) — 원칙 위반.
- 별도 데모 바이너리·스크립트 배포 — 유지보수 표면 2배, 파이프라인
  우회(검증 회로를 안 지나는 데이터 경로 생성).
- doctor가 강제 업데이트 유도(경고 상향) — 자동 값 변경 금지(§13)와
  표면 자율성 침해.

## 3. 계약

- **캐시 스키마 확장**: updateCache에 **etag 필드 추가**(checked_at은
  기존 승계 — lib/cli/update.go). 구 캐시(필드 부재)는 재 fetch.
  업데이트 공지 JSON에 checked_at 노출.
- **doctor 등록표**: checkVersions 1종(이름·임계·안내 문장 등록표).
- **커맨드 표**: `flint seed --demo [--reset]` 추가 — usage·파서 통합·
  커맨드 표 가드 동반.
- **thresholds**: doctor 경고용 캐시 나이 임계 1종(코드 기본값).
- **문서 동반**: QUICKSTART·README 설치 절·setup 완료 안내 문구.

## 4. 마일스톤(투두 — 릴리스 편성 미정, 착수 시 결정)

1. **M1 캐시 3원칙** — ETag 재검증·모순 검출기·checked_at 노출.
   검증: 캐시 하위호환·304 경로 단위 테스트(가짜 응답).
2. **M2 seed --demo** — embed·격리 영역·--reset·setup/QUICKSTART 문구.
   검증: 합성 시드 왕복 e2e·멱등 재실행·격리 침범 부재 테스트.
3. **M3 doctor 정합 표시** — checkVersions·표면 자가 보고(훅 meta 기록)·
   mcp-registry 대조. 검증: 등록표 동질 가드·오프라인 조용히 경로.

## 5. 미결정 (착수 시 결정)

- 데모 격리 메커니즘 확정 — 별도 데이터베이스(동일 엔진·DSN db 전환)
  vs 별도 FLINT_HOME(포트 충돌 처리 필요). 권장: 별도 DB(신규 인프라 0).
- 자가 보고 meta 키 명명·표면 구분 키(hook 종류별).
- mcp-registry 대조의 오프라인 동작(캐시 전용 표시 여부).

## 6. 비목표

- 자동 업데이트 강제·실행(공지와 사람 판단 유지 — knowledge-pipelines
  §12 제안 회로와 동일 원칙).
- 레지스트리 폴링 데몬·상시 버전 감시.
- 데모 데이터의 실DB 혼입·개인 데이터 예시화.
- brew tap 개설(별도 이월 항목 — 본 플랜 범위 외).

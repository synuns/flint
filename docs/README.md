# flint 문서 지도 · 관리법

> docs/의 색인과 유지 규칙. 새 문서를 만들기 전에 이 문서의 배치 규칙(§2)을
> 따른다. 문서 정합성은 `internal/doclint`(go test)이 게이트한다(§5).

## 1. 문서 지도

| 분류 | 문서 | 용도 |
|---|---|---|
| **사용자** | [QUICKSTART.md](QUICKSTART.md) | 5분 설치·첫 사용 안내 |
| **운영** | [RUNBOOK.md](RUNBOOK.md) | 진단·복구·감사 등 운영 절차 |
| | [TUNING.md](TUNING.md) | 임계값 튜닝 가이드(기본값 + meta 오버라이드) |
| | [METRICS.md](METRICS.md) | 효과 계측 기준(지표 정의·라벨) |
| | [PERF.md](PERF.md) | 성능 측정 관례·벤치마크 |
| **기여** | [adr/](adr/) | 아키텍처 결정 기록(ADR) |
| | [contracts/](contracts/) | 표면 계약 — cli·hooks·mcp·bridge·plugin·registry |
| | [adapters/](adapters/) | 하니스별 연결 가이드(17종) |
| | [TESTING.md](TESTING.md) | 테스트 작성 규약 |
| | [HISTORY.md](HISTORY.md) | 버전 이력(체인지로그) |
| **내부** | [PLAN.md](PLAN.md) | 기획서·로드맵(마일스톤 원천) |
| | [plans/](plans/) | 버전별 설계 문서(수명주기 §3) |
| | [LOOP.md](LOOP.md) | 개발 루프 설계(TDD·게이트·중단 조건) |
| | [CONTENT.md](CONTENT.md) | 기록 기준 카탈로그(저장/비저장 경계) |
| | [CI_REVIEW.md](CI_REVIEW.md) | CI 설계 리뷰 |
| | [BRAND.md](BRAND.md) | 브랜드·디자인 지침 |

## 2. 배치·네이밍 규칙

| 종류 | 위치 | 네이밍 |
|---|---|---|
| 루트 가이드 | `docs/` | 대문자 스네이크 단일 파일(`RUNBOOK.md`형) |
| ADR | `docs/adr/` | `NNNN-slug.md` — 4자리 연번, `ADR-` 접두 없음 |
| 표면 계약 | `docs/contracts/` | `<surface>.md`(소문자) |
| 어댑터 가이드 | `docs/adapters/` | `<harness>.md`(소문자) |
| 버전 설계 | `docs/plans/` | `YYYY-MM-DD-flint-vX.Y.Z.md`(점 구분 버전) |

- 버전 표기는 항상 점 구분(`v1.11.0`). `v1110` 같은 무점 표기는
  기존 파일명에만 남아 있고 신규 금지.
- 문서 간 링크는 repo 루트 기준 상대경로(`docs/adr/0009-…`)로 쓴다.

## 3. plans/ 수명주기 · 에이전트 진입 프로토콜

**색인**: [plans/README.md](plans/README.md) — 활성 플랜 상태 표
(플랜 · 상태 · 요약 · 다음 액션 · 갱신일).

**수명주기**: `draft → active →` 릴리스 확정 시 `archive/YYYY-MM/`로
이동 · 폐기·거절 시 `closed/`(사유 보존 — 재론 방지 회수 원천). 색인 표와
플랜 머리 `상태:` 줄의 동기는 internal/doclint 게이트(plans_index_test)가
강제한다.

**에이전트 프로토콜**(하니스 중립 — 어떤 에이전트 세션도 동일하게):

1. 세션 시작 — 색인 표를 읽는다(전문 로드는 담당 플랜만).
2. 작업 — 담당 플랜의 상태·다음 액션을 준수한다.
3. 종료 — 색인 표와 머리 `상태:` 줄을 갱신한다.
4. 릴리스·폐기 확정 — `archive/`·`closed/`로 옮기고 색인에서 제거한다.

## 4. ADR 규칙

- 연번은 단조 증가, 건너뛰어도 되돌리지 않는다(2026-11-21 전면 네이밍
  통일 `NNNN-slug.md` — ADR-0011~0016도 접두 제거 완료).
- 새 ADR은 짧은 맥락·결정·결과로만. supersede 시 본문 상태 표기.

## 5. 품질 게이트

- 문서 정합성 테스트(README 표·버전 배지·계약 문서 동기 등)는
  `internal/doclint`(패키지 `doclint`)에 둔다 — docs/에는 마크다운만.
- 테스트의 파일 경로는 docs 패키지 시절 표기를 그대로 쓰고
  `docPath()` 헬퍼가 실제 위치를 해석한다(내용 단얱 리터럴과 혼동 방지).
- `make ci`로 전체가 자동 검증된다.

## 6. 유지 원칙

- 문서 예시·픽스처는 합성 데모 데이터만(저장소 최상위 원칙 — CLAUDE.md).
- 실측 수치·개인 운영 기록은 문서에 인용하지 않는다.
- README는 EN/KO 2본 동기, docs/ 본문은 한국어.

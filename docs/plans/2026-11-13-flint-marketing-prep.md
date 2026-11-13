# 마케팅 준비 조사·브레인스토밍 (2026-11-13)

> 사용자 지시로 수행한 조사+브레인스토밍 정리. 개발 미착수 —
> v1.3.0(문서 계열)·v1.4.0(기능 계열) 편성 포함.
> 원칙 상기: 실DB 수치·개인 데이터 비인용, 공개 산출물은 합성
> 픽스처만, 개인 튜닝값 repo 불침투.

## 1. 배경

해외 채널 마케팅 전제(i18n·영어 디폴트는 v1.3.0에 편성).
본 문서는 그 외 마케팅 준비에 필요한 기능·작업 후보를 정리한다.

## 2. 현재 준비도 (2026-11-13 조사)

- README — EN+KO 이중어·로고·벨지·훅 카피("amnesia colleague")
- 설치 3경로 — plugin marketplace(`flint@flint`)·`go install`·
  릴리스 바이너리(cosign 서명·checksums)
- 온보딩 — `flint setup` 원커맨드(compose+migrate+seed 안내),
  합성 시드(data/seed.jsonl)
- 다이어그램 4종(EN/KO)·어댑터 문서 8하네스·MCP native·자가 업데이트

## 3. 작업 항목 (사용자 제시 11종)

| # | 항목 | 판단·해법 |
|---|---|---|
| 1 | README 고도화 | 효능 섹션·GIF 임베드·비교표·소셜 프리뷰. v1.3.0 영어 전환과 결합 |
| 2 | 효능·수치 제안 | METRICS 금지조항(실측 미인용) → **재현 가능 합성 벤치마크**: 시나리오+스크립트 공개, 방법론 자체를 차별점으로 |
| 3 | 설치 확장(brew 등) | §7 비목표 반전. 단계: curl 인스톨러 → self-hosted tap → 본체 등재 신청. linux 배포는 재론 보류 |
| 4 | 대시보드 고도화 | 데스크톱 앱 라이크 디자인. React는 **사전 빌드 번들 vendor 임베드** 전제로만(Go 단일 빌드 유지 — v0.36.0 Node 기각 사유 준수). 양안: CSS만 고도화 vs React 번들 |
| 5 | 그래픽 점검 | 소셜 프리뷰 이미지(부재)·og:image·GIF(합성 픽스처)·README 구버전 문자열 스윕 |
| 6 | 데모 체험 | 최대 마찰=docker+Doltgres 필수. 추천: asciinema 캐스트 + Codespaces 원클릭 우선, `flint demo` 내장 모드는 후속 |
| 7 | 신뢰성 증명 | 기존: cosign 서명·체크섬·프라이버시 계층(secrets 스캔·마스킹·살균 조크포인트). 추가: SECURITY.md·SBOM 첨부·govulncheck/CodeQL CI·OpenSSF Scorecard. 포지션: "로컬 전용 — 데이터 미유출" |
| 8 | 정적 웹페이지 | 기능 소개 사이트. GitHub Pages vs 도메인 구매 — **필수 아님**(양안 기록). 사이트는 정적 생성만(동적 서버 비목표 유지) |
| 9 | CLI TUI 설계 | §7 비목표 반전(설계 착수). 사람 전용 표면 — 지식 탐색·pending 검수·report 열람 상호작션. 후보 프레임워크: bubbletea 계열. 에이전트 표면(한 줄 JSON·비간섭)과 엄격 분리 |
| 10 | UX 이중 트랙 | **에이전트 UX**: 비간섭 5원칙·토큰 예산(G5)·--json 계약·exit 코드 불변. **사람 UX**: TUI·웹·report --html·언어 설정. 두 트랙이 서면을 침범하지 않는 게 설계 원칙 |
| 11 | 라이센스 명시 | **LICENSE 파일 부재(2026-11-13 확인)** — 법적 기본=all rights reserved라 오픈소스·재배포·의존 채택이 막힘. LICENSE 추가(종류 미결정 — MIT/Apache-2.0 제안)·README·문서 하단 명시·의존성 라이센스 고지. 신뢰·마케팅 최우선 갭 |

## 4. 마일스톤 편성 (2026-11-13 확정, 동일일 재번호)

> 최초 편성은 v1.2.0/v1.3.0이었으나 v1.2.0이 관측 고도화 릴리스로
> 사용됨(PR #153) — 문서 계열 v1.3.0·기능 계열 v1.4.0으로 재번호.

- **v1.3.0**: i18n·영어 디폴트·지식 export + 문서 계열(1·2·5·11 —
  README 고도화·합성 벤치마크 방법론·그래픽·라이센스)
- **v1.4.0**: 기능 계열 — 6 데모·4 대시보드 고도화·
  9 TUI 설계·3 설치 확장·7 신뢰 아티팩트·8 정적 웹(필수 아님)·
  10 UX 이중 트랙 원칙 반영

## 4.1 i18n·export 설계 조사 (v1.3.0 상세)

- seam 선행 — 사용자 노출 문자열 전부 t(msgid,args) 단일 래퍼로
  회수. 영어 단일 상태에서 시작(카탈로그는 후속 — 나중 회수는 고비용)
- 범위 — 번역: help 본문·진행/에러 메시지·report 라벨. 비번역:
  명령·플래그·설정 키·--json 필드명·exit 코드·로그(검색성)
- 카탈로그 — 키 기반(err.not_found+ko.json) vs gettext(msgid=영어
  문장, 미번역 시 영어 폴백) 택일. Go 후보: nicksnyder/go-i18n(키+ICU
  복수형)·x/text/message·gotext. 문자열 조립 금지 — placeholder
  전체 문장 템플릿, 복수형은 ICU MessageFormat
- 언어 인식·변경 UX — 감지 체인: --lang > 저장 설정 > LANGUAGE >
  LC_ALL > LC_MESSAGES > LANG > 기본 en. ko-KR→ko→en 폴백. 별도
  조작 없으면 터미널 로케일 자동 추종, 변경은 지속·즉시 적용.
  설정값은 meta 오버라이드(개인값 운영 DB — repo 불침투)
- 영어 디폴트 전환 — CLI 출력·하네스 접점([flint] 주입·nudge·갱신
  안내)·docs/ 본문. 한국어는 ko 카탈로그·번역 제공(기존 한국어
  기록 소급 없음)
- 터미널 특유 — CJK 전각 표 정렬 wcwidth(mattn/go-runewidth)·
  날짜·숫자 CLDR(x/text)
- 지식 export 계약 — 파일 형식은 옵션 지정. 이관 교환(JSONL·
  flint import와 쌍)·문서 변환(Markdown)·살균 덤프(render Masked
  계층)·DB 덤프(완전 백업·복원). 상세는 스펙 확정 시 정의

## 5. 미결정 (착수 시 결정)

- 라이센스 종류(MIT vs Apache-2.0 — 의존성(pgx·x/text)과 양립)
- React 채택 여부(vendored 전제) vs CSS 고도화
- 정적 웹: GitHub Pages vs 도메인(필수 아님 — 이름 충돌 재스윕
  ADR-0003과 연동)
- brew 본체 등재 시점(tap 선행)
- TUI 프레임워크·스코프(전 명령 vs 검수·탐색 중심)
- 벤치마크 시나리오 설계(합성 데이터 기반)

## 6. 비목표 반전 기록 (2026-11-13 사용자 지시)

- brew 본체 등재 — §7 비목표에서 제외(단계적: tap → 본체)
- TUI — §7 "TUI/대시보드 서버"에서 TUI 설계는 대상으로 이동.
  대시보드 동적 서버·linux 배포는 비목표 유지

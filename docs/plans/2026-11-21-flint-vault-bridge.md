# flint 볼트 브리지 — 옵시디언 인덱싱·인사이트 증류 (2026-11-21)

상태: active
원천: docs/PLAN.md §1(흐름 규칙·전제)·§6 — 2026-11-21 갭 점검에서 플랜 전 계층에서
누락된 것으로 확인(코드·docs/adapters 17종·contracts에 `obsidian|vault` 0건 —
어댑터는 전부 하네스 계열).
준수: CLAUDE.md(비간섭 5원칙·합성 데이터) · LOOP.md(TDD·SOLID·게이트) ·
ADR-0013(위협 모델) · ADR-0018(신규 — 증류 출력 계약, 착수 시 작성)

> 2026-11-21 기획 리뷰 1차 심사(CHANGES_REQUIRED) 반영 완료 — 커밋 단위·
> 소유 판정·경로 지속성 계약 보강. 릴리스 편성은 미정 — 본문 마일스톤은
> 투두로 유지하고 착수 시 결정한다(§4). 개인 볼트 경로·실측 수치는 본문에
> 두지 않는다(예시는 전부 합성).

## 0. 문제

- PLAN §1의 창립 전제 — **옵시디언 = 인간용 원본의 유일한 소스 오브 트루스,
  flint = 에이전트가 자율 관리하는 파생 계층** — 가 문서에만 존재하고 구현이
  0이다. 현행 수입 경로는 import(규칙 파일)뿐이라 "원본→파생" 방향이 없다.
- 파생 계층이 원본을 모르므로 knowledge-pipelines §5의 낡음 신호 ⑤(근원
  변화 — 원본 해시 불일치 = 낡음)도 발화 원천이 없다.
- 역방향("다듬을 인사이트" 증류만) 역시 출력 표면이 없다.

## 1. 레퍼런스 조사 (2026-11-21, 웹)

**파일 변경 감지 — 워처 vs 폴링**
- fsnotify 공식 문서 — 파일 단위 감시는 비권장(원자 치환 rename으로 깨짐),
  재귀 감시 미지원, 감시 경로당 fd 소모.
  https://pkg.go.dev/github.com/fsnotify/fsnotify
- fsnotify "Polling fallback" 이슈 — 2014년부터 오픈. inotify 계열 한계를
  회피하는 도구들은 mtime+크기 비교 폴링을 쓴다.
  https://github.com/fsnotify/fsnotify/issues/9

**옵시디언 생태계의 색인 관례**
- 옵시디언 플러그인은 앱 내부 `metadataCache`를 소비한다(외부 프로세스는
  접근 불가 — 외부 도구는 마크다운을 직접 파싱해야 함).
  https://forum.obsidian.md/t/wikilinks-in-yaml-front-matter/10052
- 프론트매터 wikilink는 YAML 따옴표 관례가 필요 — 파서는 이 변형을
  수용해야 함. https://github.com/mnaoumov/obsidian-frontmatter-markdown-links

**동형 선행**
- Zep/Graphiti의 bi-temporal 수입 무효화(knowledge-pipelines §11 조사 승계) —
  원본-파생 불일치 감지의 관계적 처리 선례.

## 2. 설계 — 채택: 풀 기반 스냅숏-디프 + 포인터 인덱스

**채택(우아함 준거 — 신규 상주 부품 0·판정 1회·수축하는 배치)**

1. **스냅숏-디프 폴링** — 상주 워처 없이 `flint index` 실행 시점에 전체
   워크. 1차 필터는 mtime+size, 불일치 파일만 content_hash 재계산.
   - 근거: fsnotify의 원자 치환·재귀·fd 한계(§1), 동기화 드라이브
     (클라우드 볼트가 일반적)·네트워크 FS에서 이벤트 감시가 불안함.
   - 비간섭 정합: 데몬 부재 = flint가 백그라운드 자원을 점유하지 않는다.
   - 스캔은 만남 순간 판정과 동일 원리 — 커맨드 시점에 문맥이 무료.
2. **포인터 인덱스 — 원문 비복사** — vault_files 행은 경로·제목·프론트매터
   태그·mtime·size·content_hash·**결정론 요약**(헤딩·첫 문단 추출, rune
   상한)만 저장. 전문 복사 금지는 PLAN §1 준수. 요약 추출은 결정론만
   (외부 LLM 의존 금지 — knowledge-pipelines 비목표 승계).
   - 파생 행의 회상 가치: 에이전트는 요약으로 판단하고 원본 파일을
     직접 읽는다 — 원본 소스 오브 트루스가 훼손되지 않는다.
3. **변경 검출 → 낡음 연계** — 스냅숏 디프에서 원본 content_hash 변화는
   낡음 신호 ⑤로 발화(파생 지식 재점검 큐 — knowledge-pipelines §5 접속).
4. **증류 출력(distill)** — 엄선 지식을 마크다운 인사이트 파일로 출력.
   - 출력은 **지정 폴더 한정**(사용자가 볼트 안 inbox 폴더를 지정할 수
     있음 — 옵시디언 inbox 관례). 볼트 노트 직접 수정·삭제 금지.
   - flint→외부 파일 쓰기는 신규 표면이므로 **ADR-0018**로 계약 명문화
     (ADR-0015의 원격 쓰기 금지와 구분되는 로컬 한정 출력).
   - 멱등: 출력 완료 행에 distilled_at 기록, 재실행 시 미출력 분만.

**기각 대안(근거 명시)**
- fsnotify 상주 워처 — §1 취약점 + 데몬 = 비간섭 원칙 위반.
- 옵시디언 플러그인 경유 색인 — metadataCache는 앱 내부 전용, 외부
  바이너리가 소비 불가. flint가 플러그인을 요구하는 것은 배포 결합.
- 전문 저장(full-text copy) — PLAN §1 복사 금지 위반·원본 지위 훼손·
  토큰 예산 침범.

## 3. 계약

- **커맨드**(커맨드 표·usage 가드 동반 — 단일 소스 원칙):
  - `flint index [--vault PATH] [--limit N]` — 스냅숏 색인·디프 리포트.
  - `flint distill --out DIR [--min-score N]` — 인사이트 출력.
  - status에 볼트 색인 상태 행(마지막 색인 시점·파일 수·변경 감지 수).
- **스키마**(additive, 버전 상향 동반): `vault_files`(path UNIQUE·title·
  tags·mtime·size·content_hash·summary·last_indexed_at). 쓰기는 Repo 경유
  전용 준수.
- **dolt 커밋 단위**(리뷰 보강): 색인 쓰기는 **스캔 1회 = 배치 1커밋** —
  파일별 커밋 금지(로그 폭증). 계측 테이블 별도 커밋(v1.7.0) 선례 준용,
  engine-ops 플랜의 로그 깊이 경고와 정합.
- **thresholds**(등록표·TUNING §2 동반, 기본값은 코드에만):
  `vault_scan_max_files`·`vault_summary_max_runes`·`vault_skip_dirs`
  (`.obsidian`·`.trash`·첨부 확장자)·`distill_min_score`.
- **프라이버시**: `--vault` 경로는 사용자 명시로만(자동 탐지 금지 — 의도치
  않은 개인 폴더 색인 차단). 경로·요약의 모든 출력은 render 살균 경유.
  볼트 경로 자체가 개인 정보임 — 로그·문서 비인용.
- **read 통합**: 마일스톤 M2에서 RRF 소스 확장(vault_files 요약·태그) —
  토큰 예산·limit 상한 준수, zero-hit 분해(zero_unowned/owned_miss) 확장.

## 4. 마일스톤(투두 — 릴리스 편성 미정, 착수 시 결정)

1. **M1 색인 스냅숏** — vault_files·스캔·디프·status. 검증: 합성 볼트
   픽스처(데모 노트 세트) e2e — 변경·삭제·이름변경 케이스.
2. **M2 read 통합** — RRF 소스·토큰 예산·검색 계약 테스트 동반.
3. **M3 낡음 연계** — 신호 ⑤ 발화·재점검 큐 연결(감사 회로 동반).
4. **M4 distill 출력** — ADR-0018 작성·멱등·출력 상한·살균.

각 마일스톤은 릴리스 단위 분할 가능(§4 편성 원칙 승계).

## 5. 미결정 (착수 시 결정)

- 요약 추출 규칙 상세(헤딩 계층 반영 깊이·코드펜스 제외 등).
- vault_files를 독립 표로 유지할지 source=vault 지식행으로 적립할지
  (회상 통합 방식이 좌우).
- distill 선택 스코어 종류(recall_score vs survived 기반).
- 대용량 볼트 청크 색인(--limit 이상의 연속 실행 계약·커밋 단위 연결).
- zero-hit 소유 판정(`zero_owned_floor` — knowledge 행 기준)의 vault
  소스 포함 재정의(M2 read 통합 시).
- `--vault` 경로의 지속 설정 — 매번 명시 vs meta 저장(저장 시 status
  "마지막 색인"의 볼트 귀속 확정).

## 6. 비목표

- 옵시디언 앱 내부 API 의존·실시간 감시 데몬.
- 원문 전문 저장·볼트 노트 직접 수정.
- 원격 볼트(SMB·WebDAV) 지원 — 로컬 경로만.
- 외부 LLM 요약·판정(결정론 추출만).

# flint 개발 루프 설계

- **날짜**: 2026-10-21
- **상태**: v0.1~v0.4 완료(2026-10-22) — v0.5부터 동일 루프 재적용
- **범위**: docs/PLAN.md 전 마일스톤(v0.1→v0.3 + v0.4 확장) 완료
- **전제**: AI 자율 실행(사람은 중단 조건 도달 시에만 개입)

---

## 1. 원칙 (4축)

1. **TDD 항상**: 태스크마다 red→green→refactor. 테스트 없는 제품 코드 커밋 금지.
2. **SOLID 항상**: 패키지 경계로 강제(§5). DB 의존 로직과 순수 로직 분리.
3. **게이트 최소·최적**: 검증은 3티어로 계층화, 태스크 커밋엔 단위 테스트만(30초 이내). 무거운 검증은 wave/milestone 끝에 배치 병렬.
4. **중단 조건 4축**: 성공(DoD) · 실패(재시도 소진) · 예산(시간 초과) · 무진행(같은 상태 반복). 모든 루프 레벨에 각각 정의(§7).

## 2. 실행 구조 — 4중 루프

```
L3 milestone 루프   v0.1 → v0.2 → v0.3 (각 DoD 체크리스트로 완료 판정)
 └ L2 wave 루프     계획서 슬라이스 1개 = wave (태스크 3~6개, 1세션 분량)
    └ L1 task 루프  1행동 = 1 TDD 사이클 (≤90분, 커밋 단위)
       └ L0 spike   기술검증·미정 탐색 (throwaway, 테스트·커밋 불필요, 타임박스 30분)
```

- L0 spike: 결과만 ADR/검증 기록으로 남기고 코드는 폐기. "되는가?"만 답한다.
- L1 task: superpowers TDD 스킬 그대로. 1 태스크 = 1 커밋(+회귀 테스트).
- L2 wave: superlite subagent-driven-development로 병렬 디스패치(독립 태스크 동시 실행).
  병렬 태스크는 상호 배타적 파일 소유 — 소유권 겹침은 taskgraph 에지로 직렬화.
  wave 끝에 G2 게이트 1회.
- L3 milestone: DoD 전량 충족 + 신선한 최종 리뷰(superlite:final-reviewer) 통과 시에만 다음 milestone.

**스킬 분업 (확정)**
- **루프 = superlite**: 계획(writing-plans)·디스패치(subagent-driven-development)·
  검증(verification-before-completion·final-reviewer)·마무리(finishing-a-development-branch)
- **개발 = pstack**: 코드 작성 태스크는 `/poteto-mode`가 poteto-agent에 위임해 실행.
  리뷰 시 comment-sicko가 주석 정리·워크어라운드 단속
- 충돌 시: 프로세스(게이트·중단 조건·TDD 순서)는 이 문서 우선, 코딩 관례는 pstack 우선

## 3. 태스크 파이프라인 (L1)

1. **plan**: 태스크 슬라이스 확인 + **기존 구현 탐색(ripgrep — 중복 구현 금지)** — 미정 항목 만나면 §6 프로토콜 먼저
2. **red**: 실패 테스트 작성 (계약의 실행 가능한 명세)
3. **green**: 최소 구현. DB 없이 가능하면 fake, 경계는 통합 테스트로
4. **refactor**: SOLID 위반 정리 (extract interface, 단일책임 이동)
5. **commit**: `G1 통과 → 커밋`. 메시지 = `태스크 요약 [wave:N]`

실패 처리: 같은 테스트 3회 red → **중단, 상승**(doubtful assumption 명시와 함께).

테스트 보호(구조적): 태스크 에이전트는 기존 테스트 수정·삭제 금지(신규만 작성).
완화·삭제는 리뷰어 전용 + 커밋 메시지에 사유 — "all green을 위해 테스트를 지우는"
에이전트 패턴 방어.

## 4. 검증 게이트 — 3티어

| 티어 | 시점 | 내용 | 목표 시간 |
|---|---|---|---|
| G1 | 태스크마다 | `go test` 영향 패키지만 + `go vet` | ≤30초 |
| G2 | wave마다 1회 | 통합(Doltgres docker) + E2E 시나리오 + lint 전체 | ≤5분 |
| G3 | milestone마다 1회 | 전체 E2E 3케이스(dedup/재시도/이스케이프) + `go test ./...` 전체(slow 포함, CI 패리티) + 신선한 최종 리뷰 | ≤20분 |

**최적화 규칙**
1. 영향 받은 패키지만 테스트(`go test ./lib/store/...`). 전체 스위트는 G2/G3에서만.
2. Doltgres 컨테이너는 wave 시작 시 1회 기동, wave 내 재사식(재기동 금지).
3. 독립 태스크는 병렬 subagent — G1은 각자, G2는 wave 끝 1회.
4. G2 실패 시: 실패 태스크로 bisect → 해당 태스크만 재루프(예산 §7 적용).
5. 스파이크·스캐폴딩은 게이트 밖 (제품 코드 아님).

## 5. SOLID 강제 — 패키지 경계

```
lib/
  cli/       # 파싱·출력 (exit code 계약) — 의존: store, query
  store/     # write 5단계 파이프라인 중 순수 로직(검증·정규화·판정·해시)
  query/     # read/hot 조립 (SQL 생성, 예산 적용) — 순수
  db/        # pgx 래퍼 + 재시도 0s/1s/3s + 커밋 — 인터페이스 주입
  backup/    # 덤프·복구
  mcp/       # v0.3 실장
  sqlfs/     # schema·views (go:embed — v0.1 정정: 상위 경로 접근 제약)
```

- `store`/`query`는 DB 없이 단위 테스트(빠른 G1의 원천). `db`만 실 Doltgres 필요.
- Repository 인터페이스를 `db`가 구현, `store`/`query`가 소비(의존성 역전).
- CLI 서브커맨드 1개 = 핸들러 1개(단일 책임). 새 서브커맨드 = 기존 코드 수정 없이 추가(개방-폐쇄).

## 6. 미정 항목 프로토콜 (brainstorm → ADR)

태스크 진행 중 미정·모순 만나면:

1. **식별**: 기획서의 어느 조항이 비었는지 명시
2. **3옵션 스케치**: 각각 기획 원칙(비간섭 5원칙·토큰 예산·MySQL 기피·단방향 흐름) 대조
3. **채택**: 가장 원칙 부합 1개. 15분 초과 시 최소이행 옵션으로 강제 종결
4. **기록**: `docs/adr/NNN-주제.md` (맥락·옵션·결정·되돌림 트리거) + 기획서 정정 제안
5. **상승 예외**: 사용자 가치·비용에 직결되는 결정만 사용자에게 1문화(모아서 1회)

**§15 이연 결정 — 착수 기본값**

| 항목 | 기본값 | 재방문 트리거 |
|---|---|---|
| 주입 예산 | 2,000자 유지 + 계측 코드 내장 | 2주 실측 p95 |
| 보존·삭제 | 전 보존(무효화만), 삭제 없음 | 데이터 10만 건 도달 시 |
| 백업 포맷 | pg_dump 우선, 실패 시 dolt SQL 덤프 | W0 스파이크 결과 |
| 임계값 수치 | 상수 정의 + `meta` 테이블에서 읽기 | v0.3 튜닝 |

## 7. 중단 조건 (맴돌 방지)

**각 레벨의 성공 종료**
- L1: green + refactor + G1 통과 커밋
- L2: wave 태스크 전부 G1 통과 + G2 1회 통과
- L3: DoD 체크리스트 100% + G3(최종 리뷰 pass)

**강제 중단 (에스컬레이션)**
1. **실패**: 같은 테스트/같은 오류 3회 재시도 → 중단, doubtful assumption 명시
2. **무진행**(조작적 정의): 동일 오류 시그니처(실패 테스트 ID + 에러 요약) 2회 연속,
   또는 커밋·ADR 산출 없이 30분 경과 → 중단
3. **예산 초과**: 태스크 90분 / wave 6시간(추정 2배) / milestone 12시간 초과 → 중단, 남은 것 재계획
4. **범위 침입**: 현재 wave 밖 요구 발견 → 백로그 적립 후 계획서 제안, 현재 루프에 편입 금지
5. **외부 승인**: Release 퍼블리시·홈브루 tap 공개 등 외부향 행위 → 사용자 확인 필수

중단 시 산출물: 무엇이 막혔는지 + 시도한 것 3개 + 다음 액션 제안. 재개는 사용자 승인 후.

## 8. 마일스톤 DoD

**v0.1** — 코어 루프
- [x] W0: 검증 스파이크 4건(pg_dump·dbhub 접속·AS OF·이름 스윕) 각 ADR화
- [x] 스키마·뷰 SQL embed + meta 마이그레이션 동작
- [x] write/read/hot/status E2E 3케이스(dedup·재시도·이스케이프) 통과
- [x] 수동 백업(덤프) + 복구 1회 훈련
- [x] Claude Code 플러그인 설치·훅 동작(hot/done)
- [x] 시딩 완료(활성 프로젝트 3건 + 기획 결정 + 사용자 선호)

**v0.2** — 운영 편의
- [x] approve 정식화 / maintain 트리거 / done 임계값 경고 / 백업 자동(하루 첫 세션)

**v0.3** — 확장
- [x] flint mcp / Hermes 카탈로그·OpenClaw 어댑터 / 임계값 튜닝(p95 실측 반영)

## 9. 웨이브 브레이크다운

**v0.1 (W0~W6)**
- W0 검증 스파이크(4건, 병렬) → 백업 포맷 ADR
- W1 스캐폴딩: go module·Makefile·docker-compose(Doltgres)·SQL embed·스키마 통합 테스트
- W2 store: write 5단계(검증·dedup·판정·정규화 — 순수 단위) + exit code 계약
- W3 db: 커밋 파이프라인(재시도)·충돌 시나리오 통합 테스트
- W4 query: read/hot/status(뷰 강제·예산·JSON 출력)
- W5 backup/restore + setup/teardown + 플러그인(hooks·skill·command)
- W6 시딩 + G3 최종(3 E2E + 리뷰)

**v0.2 (W7~W9)**: approve·maintain 트리거 / done 임계값 / 백업 자동화
**v0.3 (W10~W12)**: mcp / 어댑터 / 튜닝

## 10. 산출물 규칙

- 태스크 1개 = 커밋 1개(회귀 테스트 포함). wave 1개 = 병합 or 리뷰 1회.
- 결정 = `docs/adr/` ADR. 장애·학습 = `.superlite/incidents.jsonl`.
- 매 milestone 종료: DoD 체크 결과 + 남은 부채 리포트 1페이지.
- G3 리뷰어는 diff뿐 아니라 PLAN.md §7 계약(exit code·출력 형식)을 대조 — 리뷰 기준의 점진적 하락 방지.

# flint plans — 활성 색인 · 수명주기

> 에이전틱 관리 진입점. 세션(하니스 무관)은 이 표를 먼저 읽고 필요한
> 플랜 전문만 로드한다(progressive disclosure). 색인·헤더 정합은
> internal/doclint 게이트(plans_index_test.go)가 강제한다.

## 활성 색인

| 플랜 | 상태 | 요약 | 다음 액션 | 갱신 |
|---|---|---|---|---|
| `2026-11-21-flint-release-scope.md` | active | 전체 릴리스 스코프 v1.19.0~v2.0.0 — 11 릴리스 편성·완료 루프 계약(§4)·상태 표(§5) | 루프 실행 중 — v1.19.0 발행·패치 루프(R71~R74) 진행 | 2026-11-22 |
| `2026-11-22-flint-v1190.md` | active | v1.19.0 관측 기반 계층 A1~A5 — 스키마 v13(near_score·스냅숏·주간 rollup)·doctor 신선도·diagnose 커버리지 | v1.19.0 발행 완료(2026-11-22) — 4각도 감사 진행·종결 시 archive | 2026-11-22 |
| `2026-11-22-flint-v1190.spec.md` | draft | v1.19.0 관측 기반 계층 스펙 — 리서치(v119-research) 7논점 권안 채택 | 플랜과 함께 G2 리뷰 | 2026-11-22 |
| `2026-11-19-flint-knowledge-pipelines.md` | active | 지식 관리 계열 로드맵 — K원칙·교정 채널·변동성 계층·만남 판정·관측·자율 튜닝(§12·ADR-0017) | §12 편성 확정 — 계측 v1.19.0·자율 shadow v1.23.0·적용 v1.24.0·잔여 v1.25.0(스코프 문서 §2) — 병합·환류는 계측 동반 분해 | 2026-11-21 |
| `2026-11-21-flint-vault-bridge.md` | active | 볼트 브리지 — 옵시디언 스냅숏-디프 인덱싱·포인터 저장(원문 비복사)·인사이트 증류 출력(ADR-0018) | v1.21.0(M1 색인·M2 read 통합)·v1.22.0(M3 낡음·M4 distill) 편성 확정 | 2026-11-21 |
| `2026-11-21-flint-engine-ops.md` | active | 엔진 운영 — maintain --gc(접속 단절 계약)·doctor 경고 2종(로그 깊이·db 크기)·컨테이너 footprint 표시·버전업 절차 | v1.26.0 편성 확정 — M1 GC·경고부터 | 2026-11-21 |
| `2026-11-21-flint-surface-trust.md` | active | 표면 신뢰 — 업데이트 캐시 3원칙(ETag 조건부 재검증·모순 검출기)·doctor 버전 정합 표시·`flint seed --demo` 격리 데모 | v1.27.0 편성 확정 — M1 캐시 3원칙부터 | 2026-11-21 |

> v1.12.0~v1.18.3(2026-11-21 릴리스)의 플랜·스펙은
> `archive/2026-11/`로 이동 완료 — 이력은 HISTORY.md 해당 버전 행 참조.

## 수명주기

`draft → active →` 릴리스 확정 시 `archive/YYYY-MM/` · 폐기·거절 시 `closed/`

- 상태 전환 시 이 표와 플랜 머리의 `상태:` 줄을 함께 갱신한다(게이트가
  동기를 강제).
- `closed/` — 거절·폐기 기획도 "왜 안 했는가"의 회수 원천으로 보존한다.
  본문에 사유를 남기고 삭제하지 않는다(재론 방지).
- `archive/`는 릴리스 시점의 스냅숏 — 다시 고치지 않는다. 원천은
  PLAN.md·HISTORY.md의 해당 버전 행이다.

## 신규 플랜 헤더 템플릿

```markdown
# <제목> (YYYY-MM-DD)
상태: draft
원천: docs/PLAN.md §6 <해당 행> (역링크)
준수: CLAUDE.md(비간섭·합성 데이터) · LOOP.md(루프 원칙) · ADR-<NNNN>(해당 시)
```

- 파일명은 `YYYY-MM-DD-flint-vX.Y.Z.md`(점 구분 버전 — 무점 표기 금지).
- 상태 값: `draft`(기획 중) · `active`(진행 중) · `blocked`(막힘 — 다음
  액션에 해제 조건 명시).

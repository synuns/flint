# flint plans — 활성 색인 · 수명주기

> 에이전틱 관리 진입점. 세션(하니스 무관)은 이 표를 먼저 읽고 필요한
> 플랜 전문만 로드한다(progressive disclosure). 색인·헤더 정합은
> internal/doclint 게이트(plans_index_test.go)가 강제한다.

## 활성 색인

| 플랜 | 상태 | 요약 | 다음 액션 | 갱신 |
|---|---|---|---|---|
| `2026-11-19-flint-knowledge-pipelines.md` | active | 지식 관리 계열 로드맵 — K원칙·교정 채널·모순 감지·변동성 계층·만남 판정 | 잔여 이월 항목(병합·환류) 마일스톤 분해 — 모순 감지·만남 판정은 v1.17.0 에코로 이행 | 2026-11-21 |
| `2026-11-21-flint-v1180.spec.md` | draft | v1.18.0 MCP structuredContent·outputSchema 스펙 | 리서치(v118-mcp-research) 결과 반영·플랜 승격 | 2026-11-21 |

> v1.12.0~v1.17.0(2026-11-21 릴리스)의 플랜·스펙은
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

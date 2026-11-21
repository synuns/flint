# flint 전체 릴리스 스코프 — v1.19.0~v2.0.0 편성·완료 루프 (2026-11-21)

상태: active
원천: docs/PLAN.md §6 이월 ①②③ · 활성 플랜 3종(vault-bridge·engine-ops·
surface-trust) · knowledge-pipelines §5·§12 · 사용자 루프 실행 지시
(2026-11-21 — 완료 시점까지 무인).
준수: CLAUDE.md(비간섭·합성 데이터) · LOOP.md(게이트) · ADR-0017(자율
튜닝)·ADR-0018(증류 출력 — v1.22.0 착수 시 작성)

> 이 문서가 전체 스코프의 단일 소스다. 버전당 상태를 갱신·커밋하며 루프는
> 전 행 done까지 순환한다(§4). 본문은 편성·의존·완료 판정만 담고, 버전
> 세부 기획은 각 마이너 착수 시 기획 리뷰 게이트(리서치→스펙+플랜→독립
> 리뷰)를 거쳐 `YYYY-MM-DD-flint-vX.Y.Z[.spec].md`로 작성한다.
> 독립 리뷰(2026-11-22 1차) 16건 반영 — 이행 실적 대조·계열 분할·
> 루프 탈출구 승계.

## 1. 재고 — 미완 잔여 (2026-11-22, 이행 실적 대조 완료)

| 계열 | 출처 | 잔여(실적 제외) | 상태 |
|---|---|---|---|
| 관측·자율 튜닝 | knowledge-pipelines §12(ADR-0017) | A1~A6 계측·B1~B6 회로·C1~C6 가시성·D1~D6 동반 계측 | 방향 확정·계측 미착수 |
| 지식 스키마 발전 | 2026-11-21 세션 리서치(repo 원천 부재) | read 필터 확장·supersede 재귀 뷰·knowledge_links는 **v1.20.0 스펙 승격(v1.20.0 플랜)** — 잔여 kind 열은 v1.25.0 이월(§2) | 승격 완료·잔여 이월 |
| 볼트 브리지 | vault-bridge 플랜(기획 리뷰 1차 통과) | M1 색인·M2 read 통합·M3 낡음 연계·M4 distill | 착수 대기 |
| 낡음·변동성 잔여 | knowledge-pipelines §5 — v1.13.0(age-audit·vol 태그·감쇠 면제)·v1.14.0(survived decay relief) 실적 제외 | 신호 ⑤ 근원 변화(vault_files 근거 — v1.13.0 non-goal 룰링 해제)·변동성 출처·주제 기본값 자동 분류·조건 태그·재점검 큐·재확신 처분(유효기간 리셋) | 잔여만 |
| 지식 관리 잔여 | knowledge-pipelines §5·§6 | merge-audit·환류 일반화(bad_recall→처분)·read --related·--why + D1·D2·D4 계측 동반 | 후보 |
| 엔진 운영 | engine-ops 플랜(기획 리뷰 1차 통과) | M1 GC·경고·M2 footprint·M3 ANALYZE spike | 착수 대기 |
| 표면 신뢰 | surface-trust 플랜(기획 리뷰 1차 통과) | M1 캐시 3원칙·M2 seed --demo·M3 doctor 정합 | 착수 대기 |
| 표면·품질 청산 | PLAN §6 이월②③ — v1.9.3·v1.16.0(병렬화)·v1.10.0(주석 영어화) 실적 제외 | read TUI·tuning TUI 기본 승격 검토·i18n KO 카탈로그·brew 검토·테스트 폭발 정리·이름 재스윕·서명 재실증·관측 이월 재판정(기저율 셔플 트리거) + **원천=로컬 인시던트(G2 재검): 시퀀스 회수·R68 정보 3건·Doltgres 업스트림 2건** | 이월 |

## 2. 배분 — 11 릴리스(마이너 10 + 메이저 1)

원칙: ①마이너 1개 = 계열 1주제(+동반 계측·소과제) ②계측 인프라가 최선
— 이후 전 버전이 D 계측 동반 원칙(§12.7)으로 소비 ③자율 적용 회로의
착수는 버전 순서가 아니라 **조건으로 게이트** — v1.24.0 착수 조건:
v1.23.0(shadow) 발행 후 관측 축적(적립 회차·예측 적중 건수 — 확정값은
v1.23.0 G2 등록표에서) 미달 시 **v1.25.0(독립 계열)과 순서 교환 — 교환
실행 시 §2 배분 표·§5 상태 표 비고·의존 메모를 동일 커밋에서 갱신** ④스키마
변경은 버전당 1회 additive ⑤패치 버전은 소모품 — 마이너 번호는 본 표가
고정, 패치는 소속 마이너 done에 흡수.

| 버전 | 계열·카테고리 | 내용 | 스키마 | 사이즈 |
|---|---|---|---|---|
| v1.19.0 | 관측 기반 (Observability) | §12.7 M1 — A1 read_events 스코어 열·A2 임계값 스냅숏 diff-on-write·A3 주간 rollup(분자·분모 카운터)·A4 계측 신선도·A5 커버리지 경고 | v13 additive | M |
| v1.20.0 | 지식 스키마·검색 (Schema) | read 필터 확장(`--min-confidence`·`--origin` — migration 0)·supersede 사이클 가드(maintain --supersede/--merge·import·깊이 상한 64)·`supersede_chains` 재귀 뷰(경로배열 가드)·`knowledge_links` 테이블(어휘 4종 refines/supports/contradicts/duplicates)·PAM links 왕복 — G2 승격 확정(2026-11-22-flint-v1200)·kind 열은 v1.25.0 이월(룰링) | v14 additive | M |
| v1.21.0 | 볼트 브리지 I (Integration) | vault-bridge M1 색인 스냅숏-디프(vault_files·배치 1커밋)·M2 read RRF 통합 + A6 스캔 p95 계측 동반 | v15 additive | M-L |
| v1.22.0 | 볼트 브리지 II·낡음 잔여 (Curation) | M3 낡음 신호 ⑤(v1.13.0 non-goal 룰링 해제 — vault_files content_hash 근거)·재점검 큐·M4 distill(ADR-0018 작성) + 변동성 출처·주제 기본값 자동 분류·조건 태그·**재확신 처분(유효기간 리셋 — §5 세 처분 중 미실현 잔여)**. 재점검 큐 저장이 신규 스키마를 요구하면 G2에서 v1.21.0 선행 편입 또는 차기 스키마로 이월 | — (요구 시 G2 확정) | M-L |
| v1.23.0 | 자율 튜닝 I (Autonomy-shadow) | B1 정책(화이트리스트·킬스위치)·B3 등록표(관측 축적 조건 확정 포함)·B6 tuning log·B2 shadow 단 + C1 what-if·C2 분포·C6 정합 카드 | — | M |
| v1.24.0 | 자율 튜닝 II (Autonomy-apply) | B2 신뢰 사다리 적용·B4 자동 복원·B5 센티넬 + C3 추세·C4 밴드·C5 분절 뷰 — **착수 조건 §2 원칙 ③** | — | L |
| v1.25.0 | 지식 관리 잔여 (Curation II) | merge-audit·환류 일반화(bad_recall→처분)·read --related·--why·**kind 열 이월(v1.20.0 룰링 — 소비처 확정 후 additive 스키마 동반)**·survived_decay_relief 활성화 판정(기본 0 — 관측 근거 확보 시) + D1·D2·D4 계측 동반 | additive (버전은 G2 확정) | L |
| v1.26.0 | 엔진 운영 (Ops) | engine-ops M1 maintain --gc·doctor 경고 2종·RUNBOOK 2절·meta 이력 / M2 footprint 표시·M3 ANALYZE spike(L0) | — | M |
| v1.27.0 | 표면 신뢰 (Trust) | surface-trust M1 캐시 3원칙(ETag·모순 검출기·checked_at)·M2 seed --demo(격리·--reset)·M3 doctor checkVersions(자가 보고) | — | M |
| v1.28.0 | 표면 확장·품질 청산 (Cleanup) | read TUI·tuning TUI 기본 승격 검토·i18n KO 카탈로그·brew 검토·테스트 폭발 정리·이름 재스윹·서명 재실증·관측 이월 재판정 + 로컬 원천 3건 G2 재검(시퀀스 회수·R68 정보·Doltgres 업스트림) | — | M |
| v2.0.0 | 플랫폼 전환 (Platform) | DB 선택 — PG Store 지원·audit_log 리플레이 감사 보존·스코프 C 재정의(--all-domains — PLAN §7 "다중 에이전트 domain 스코프 트리거 대기"의 해소 경로)·이행 가이드·문서 대개정 | 상속 | XL |

의존 메모: v1.20.0→v1.25.0(knowledge_links 기반 --related·kind 열 이월)·v1.19.0→
v1.23.0(C1 what-if가 A1 스코어 소비)·v1.21.0→v1.22.0(디프→낡음 신호 ⑤)
·v1.23.0→v1.24.0(원칙 ③ 조건 게이트 — 관측 축적)·v1.28.0은 v2.0.0 직전
채무 청산.

## 3. 버전당 완료 판정 (done 조건)

1. 기획 리뷰 게이트 통과(독립 리뷰 APPROVED)·플랜·스펙 repo 반영
2. 빌드 완료 — 계약 동반(등록표·커맨드 표·thresholds·docs-sync)·
   `make release-gate` green
3. 태그 발행 — 버전 동기 4곳(VERSION·README 배지·plugin.json·HISTORY)+
   versions_test 계약행. 태그 전 3종 확인(①`cat VERSION` ②`git log
   --oneline -1` ③`git rev-parse HEAD`)을 **별도 호출로 판독** —
   ①·②에서 새 버전 확인 + ③으로 태그 대상(HEAD) 확정 시에만 태그
   (체인 실행 금지)·watch-release 성공
4. **패치 루프 종결 — 발견 0인 독립 감사 1회 완료**(마이너 직후 4각도·
   패치 회차 2각도 감사. 자체검증은 회차 불인정). 비수렴 탈출구 승계
   (PLAN §6 감사 정책): 한 라운드 도출이 단일 계열의 구문 변형만 남기면
   근본 수정 또는 **한계 선언 후 종결** — 선언은 본 문서 상태 표 비고에
   기록
5. 정리 — 머지된 브랜치·워크트리 삭제·플랜 archive 이동·본 문서 상태
   표 done 커밋

전체 종결: §5 상태 표 전 행 done + 완료 보고(스코프 대비 실적·패치
루프 통계·기각·이월 잔여 목록).

## 4. 완료 루프 (마스터 루프 계약 — 무인 운용)

```
재개: 본 문서 → 최초 non-done 버전 + 상태별 재개 지점:
  gating → G2(게이트 산출물 확인 후 속행) · building → G3 ·
  released → G4 잔여(watch-release 확인) · patch-loop → G5
G2 기획 게이트: 리서치(외부 관례+내부 조사) → 스펙+플랜 작성 →
   독립 리뷰 에이전트 → CHANGES_REQUESTED 반영 → APPROVED
   (원천 플랜의 §미결정 항목은 이 단계에서 Ruling 확정)
G3 빌드: TDD·서브에이전트 파견·계약·문서 동반(docs-sync)
G4 발행: release-gate → 태그(§3.3 3종 별도 판독) →
   watch-release.sh
G5 패치 루프: 독립 감사 → 발견 → 패치 버전 발행 → 재감사 →
   발견 0 종결 (§3.4)
G6 정리: §3.5 → 다음 버전 G2
```

무인 운용 규칙 (사용자 2026-11-21 지시 — "패치루프도 무조건·완료
시점까지"):

- 루프는 v2.0.0 G6 완료까지 자율 순환 — 버전 간 사용자 확인 정지 없음
  (본 문서 커밋이 스코프·편성 승인의 원천).
- 세션 절단은 버전 경계(G6 직후)에서만. 컨텍스트 압박 시 현재 버전을
  G6까지 완결하고 종료 — compaction 후 본 문서 상태 표로 재개.
- CI 플랫폼 장애 시 로컬 `make ci`·release-gate green을 근거로 진행·
  Actions 회복 후 소급 검증(확립 룰링 승계).
- 외부·파괴·보안 민감 행위는 기존 게이트 유지(머지는 green CI 후).
- 정지 조건: 사용자 개입 지시·복구 불가한 외부 장애. 그 외 자율 판단은
  Ruling을 이 문서 커밋 메시지에 기록.

## 5. 상태 표 (재개 지점 — 갱신 시 커밋)

| 버전 | 상태 | 비고 |
|---|---|---|
| v1.19.0 | done | 관측 기반 — v1.19.0~.6 발행(2026-11-22)·감사 R71~R83 13회차·**R83 발견 0으로 패치 루프 종결**. 발견 총 high 2·med 5·low/기록 다수 — 전 수정. 잔여 백로그: lib/cli real 모드 위임 테스트 선행 실패 6건(테스트 인프라 — v1.28.0 청산 편입) |
| v1.20.0 | building | 지식 스키마 — G2 승인(v1.20.0 스펙·플랜)·빌드 중 |
| v1.21.0 | todo | 볼트 I |
| v1.22.0 | todo | 볼트 II·낡음 잔여 |
| v1.23.0 | todo | 자율 shadow |
| v1.24.0 | todo | 자율 적용 — 원칙 ③ 조건 게이트 |
| v1.25.0 | todo | 지식 잔여 |
| v1.26.0 | todo | 엔진 운영 |
| v1.27.0 | todo | 표면 신뢰 |
| v1.28.0 | todo | 표면·품질 청산 |
| v2.0.0 | todo | 플랫폼 전환 — 직전 버전 전부 done 후 착수 |

상태 값: `todo → gating → building → released → patch-loop → done`.

## 6. 비편성 (트리거 대기 — 루프 밖)

- census 잔여 청소(§6)·D5 계측 — echo·재점검 정착 후 판단(§8 승계)
- pending 일괄 검수·어댑터 확장(knowledge-pipelines §8 트리거 대기 승계)
- 토큰 최적화 후보 3종(§7 — summary-only 회상·zero-hit 감소·주입
  프로필 확장) — 트리거 대기 승계
- read LIKE 개량 — 엔진(pg_trgm) 지원 시 재판정(PLAN §6 이월②·
  v1.18.0 루링 "2026-11 미지원 확인" 승계 — engine-ops §2.3과 동일
  엔진 게이트 패턴)
- 마케팅 계열·brew tap 개설 — 사용자 요청 시 (v1.28.0은 "검토"만)
- pgvector(ADR-0011)·원격 MCP 쓰기(ADR-0015)·METRICS.md 개편·ADR 재편
  ·대시보드 동적 서버(PLAN §7 비목표 승계)

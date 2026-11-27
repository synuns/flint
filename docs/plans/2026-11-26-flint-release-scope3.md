# 릴리스 스코프 3기 — 미완 플랜 편성 (2026-11-26)

상태: active

- **근거**: 미완 플랜 전수 재고 — knowledge-pipelines §5·§6·§8 잔여·PLAN §6 이월②③·
  성장 모델 2단계(2026-11-25 설계 §8)·v1.27.0 조건 게이트·v1.38.0 빌드 신규 패치 후보
- **원칙 승계**: release-scope 1기 §2 그대로 — 마이너 1개=계열 1주제(+동반 계측)·
  스키마 버전당 1회 additive·조건 게이트 착수·패치는 소모품(마이너 done에 흡수)·
  done 조건 §3 승계(기획 리뷰 게이트→release-gate green→태그 4곳→패치 루프 발견 0→정리)

## 1. 배분

| 버전 | 주제 | 내용 | 스키마 | 출처 |
|---|---|---|---|---|
| v1.38.0 | 성장 모델 1단계 | 진행 중 — claim·work_state·수집 3단·검증자 | v20 | 성장 모델 설계 §8 |
| v1.38.x | 패치 루프 | 독립 감사 발견 0까지 + **e2e 엔진 격리 수리**(FLINT_CONFIG_DIR 미격리 라우팅 함정 — v1.29 격리 계약 구멍)·tests/regression 공유 엔진 경합 flap 정리 | — | v1.38 빌드 신규 |
| v1.39.0 | 성장 모델 2단계 — 환류·순위 | 참여 카운터→회상 순위 반영(decay 산식 결합)·환류 일반화(bad_recall→처분) 병합·프로젝트 연결 스키마(sessions↔project — hot 프로젝트 우선 발동) | v21 | 성장 모델 설계 §8·pipelines §6 환류 |
| v1.40.0 | 낡음·변동성 잔여 | **리서치로 축소(2026-11-28)**: claim×변동성 매핑(태그 규약·제안만)·변동성 출처 축(source 인자)·자동 분류 적용 경로(--apply-vol 승인제) — 재확신 처분(v1.22.0 실증)·재점검 큐·근원 변화(폐쇄 완성)는 스코프 제외 | — | pipelines §5 잔여 + docs/plans/2026-11-28-flint-v1400-staleness.md |
| v1.41.0 | 품질·성능 청산 | **리서치로 축소(2026-11-28)**: 테스트 통합 확장(v1.28 4조·버전 접미 154파일 중 우선 도메인)·report 잔여 병렬화 — profile 병렬화 v1.16.0 실현·read LIKE는 확장 프로브 신설 전제로 이월 | — | docs/plans/2026-11-28-flint-v1410-quality.md |
| v1.42.0 | 표면·배포 | 서명 재실증·이름 재스윕·관측 이월 재판정(기저율 셔플 트리거)·brew·마케팅(사용자 트리거 항목은 해당 시점 확인) | — | PLAN §6 이월③ |
| v1.27.0 | 자율 적용 | **조건 게이트**: shadow 라운드 ≥14·발화 ≥5·streak ≥3 충족 시 별도 착수(기존 번호 유지) | — | 1기 원칙 ③ |

## 2. 트리거 대기 (루프 밖)

census 잔여 청소·D5 계측(echo·재점검 정착 후)·토큰 최적화 3종(summary-only 회상·
zero-hit 감소·주입 프로필 확장)·어댑터 확장·마케팅 계열·brew tap 개설(사용자 요청 시).

## 3. 상태 표 (재개 지점)

| 버전 | 상태 | 비고 |
|---|---|---|
| v1.38.0 | done | **발행 완료(2026-11-26 15:53 UTC)** — release-gate green·태그 v1.38.0(HEAD b923a0c)·CI 전 job green(3회 재판정: gofmt 3파일·e2e 정렬 6종·실코드 결함 2건 수리 과정 포함)·전수 GREEN 20/0/183s |
| v1.38.x | done | **패치 루프 완결 — v1.38.1(r143-claim-integrity·00:18)·v1.38.2(r143-angle2-security·03:33)·v1.38.3(r143-angle3-performance·04:41)·v1.38.4(r143-angle4-docs·04:59) 연속 발행·전부 Latest.** R143 독립 감사 4각도 완결: 각도1(계약·스키마) 5건·각도2(보안·개인데이터) 4건·각도3(성능·자원) 5건+참고 2·각도4(문서·계약) 6건 — 전부 수리+회귀 테스트. **확정 과제 승계: release_gate.sh↔make ci 커버리지 불일치(게이트 동등성 수리 — CI 3회 실패 근본)** |
| v1.39.0 | done | **발행 완료(2026-11-27 07:26 UTC·Latest·첫 시도 CI 통과)** — 성장 모델 2단계: recall 신호 read 랭킹 합류(5번째 RRF)·recall-audit recommend(3종·recall_audit_recommend_min 68번째 키)·세션 사슬(스키마 v21 session_label·state open 라벨 수신·hot 프로젝트 우선 부활)·release-gate CI 동등성 첫 적용 — 리서치(12사실)→기획(감사 6건 반영)→superlite 6태스크·전수 GREEN 20/0 |
| v1.39.x | done | **패치 루프 완결 — v1.39.1(r143-angle2-sessionlabel·08:40 UTC·Latest) 발행.** 감사 3건 수리(session_label secrets 스캔 5열·살균 루프 편입·재보고 백필·라벨 폴백 검증 200자 상한)·14회차 전수 GREEN 20/0·엔진 위생 절차 확립(scratch purge→재기동→베이스라인 확인) |
| v1.40.0 | done | **발행 완료(2026-11-27 16:24 UTC·Latest·첫 시도 CI 통과) — 패치 루프 4각도 통합 감사 APPROVED(발견 0·LOW 정보성 1건 기록).** claim→vol 기본값 제안(감쇠 회로 무변경·자동 면제 없음·감사 HIGH 반영)·출처 축(ForSource)·--age-audit --apply-vol [--yes] 승인 경로·기획 감사 3건 반영 — 리서치(10사실·재확신 v1.22.0 실증 배분 축소)→superlite 5태스크·실DB 왕복 검증 |
| v1.41.0 | done | **발행 완료(2026-11-27 17:49 UTC·Latest)** — 테스트 4도메인 통합(36파일→4·함수명·수 불변·카운트 단언)·report 병렬 잔여(collectDaily 5-way·profile 메타 2-way·골든 무변화)·read LIKE 이월(확장 프로브 전제) — 리서치(10사실)→기획(감사 APPROVED)→5태스크 |
| v1.41.x | done | **v1.41.1(quality-consolidation-patch·18:02 UTC·Latest) 발행.** 패치 감사 3건(LOW 2 수리·이력 이탈 기록): TUNING/slowlock 낡은 참조 갱신 |
| v1.42.0 | building | 기술 항목만 진행(사용자 결정 2026-11-28·brew/마케팅 제외) — 기획 문서 docs/plans/2026-11-28-flint-v1420-surface.md·독립 감사 대기 |
| v1.27.0 | gating | 조건 미충족 — 충족 시 별도 착수 |

종료 시점: 위 전 행 done 또는 gating/closed 전환 + 완료 보고(스코프 대비 실적·
패치 통계·이월 잔여 목록) — 1기 §3 계약 승계.

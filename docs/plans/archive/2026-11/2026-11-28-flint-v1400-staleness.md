# v1.40.0 낡음 잔여 기획 — claim×변동성 매핑·출처 축·자동 분류 (2026-11-28)

상태: active

- **선행**: 리서치 완료(2026-11-28 — 10사실·근거 파일:행)·scope3 배분 갱신 필요
- **배분 전제 수정**: pipelines §5의 재확신 처분은 v1.22.0 `--reaffirm`으로
  이미 실현(리서치 과제 1 — 스코프에서 제외·scope3 표 갱신). 근원 변화
  신호는 폐쇄 회로 완성(스캔→burst 큐→recheck 해제) — 잔여는 claim 결합
  선택만. **실 잔여 3건**: ①변동성 출처 축 ②자동 분류 적용 경로
  ③claim×변동성 매핑.

## 1. 판정 — 핵심 결정 3건

1. **claim×변동성 매핑은 태그 규약 제안으로만 조립(스키마 불변·자동 면제 신설
   없음)** — 근거: claim은 정준 축(리서치 7)·vol 태그는 `:vol:x:` 규약(리서치 9)·
   claim·conditions 열 존재(리서치 10). 설계: claim→vol 기본값 표(decision→불변
   우선·procedure→조건부·lesson→조건부·event→불변 우선)을 `suggested_vol`
   제안에 반영 — **전 경로 제안만**(승인제·`--apply-vol` 경유). 오버라이드는
   기존 vol 태그 우선(매핑은 미태깅 행의 기본값). **EffectiveScoreFor·
   maintain_recall의 감쇠 면제 판정은 무변경** — event 행의 면제는 승인 적용된
   `:vol:immutable:` 태그로만 발생(감사 1호 반영: backfill event 전 행 자동
   면제가 감쇠 회로를 무력화하는 과적용 방지·기획 자체의 "제안만" 원칙과
   일관).
2. **변동성 출처 축**: `SuggestVolClassForSource(topic, tags, source)` 신규
   함수 분리(기존 SuggestVolClass 시그니처 유지·내부 위임 — Go 오버로드
   부재·기존 호출 동작 동등, 감사 2호). 출처별 기본값:
   human/correction→불변 우선·import→조건부·vault→조건부·agent→현행 규칙.
   출처 축도 제안만.
3. **자동 분류 적용 경로**: age-audit의 `suggested_vol` 제안(maintain_recall.go:319)
   에 `--apply-vol`(승인제·`--yes` 필수) 적용 경로 신설 — 제안→검수→적용의
   마지막 고리. 적용은 `:vol:x:` 태그 병합 갱신(supersede 아님).

## 2. 계약(불변)

P1·P2·K1~K5·비간섭 5원칙·승인제(자동 active 승격 금지)·thresholds 신규값은
등록+meta 오버라이드만·주석 금지·개인 데이터 불침투·기존 vol 태그·immutable
면제 회귀 유지.

## 3. 수용 기준(요지)

1. claim 기본값 표가 미태깅 행의 suggested_vol에 반영·승인 적용 후에만
   감쇠 면제 발생(자동 면제 이탈 0건 — EffectiveScoreFor 무변경 회귀로
   검증).
2. `--apply-vol --yes` 경로: 제안 승인 적용·태그 병합 검증(회귀 테스트).
3. SuggestVolClassForSource 신규 — 기존 SuggestVolClass 호출 동작 동등
   (동일 입력 동일 출력 단언).
4. 면제 이탈 상한 검증: 승인 적용 전 감쇠 대상 행 수 변동 0(감사 1호
   반영 — 적용은 승인제라 사후 계량).
5. 스키마 불변·전수 GREEN·release-gate(수리판)+로컬 make ci green.

## 4. 비목표

재확신 처분(v1.22.0 실현)·근원 변화 회로(폐쇄 완성)·자동 태깅(승인제 유지)·
세션 사슬 확장·v1.41 품질 청산 과제.

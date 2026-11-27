# v1.40.0 낡음 잔여 기획 — claim×변동성 매핑·출처 축·자동 분류 (2026-11-28)

상태: active

- **선행**: 리서치 완료(2026-11-28 — 10사실·근거 파일:행)·scope3 배분 갱신 필요
- **배분 전제 수정**: pipelines §5의 재확신 처분은 v1.22.0 `--reaffirm`으로
  이미 실현(리서치 과제 1 — 스코프에서 제외·scope3 표 갱신). 근원 변화
  신호는 폐쇄 회로 완성(스캔→burst 큐→recheck 해제) — 잔여는 claim 결합
  선택만. **실 잔여 3건**: ①변동성 출처 축 ②자동 분류 적용 경로
  ③claim×변동성 매핑.

## 1. 판정 — 핵심 결정 3건

1. **claim×변동성 매핑은 태그 규약으로 조립(스키마 불변)** — 근거: claim은
   pending 강제·승격 게이트·read 필터를 갖춘 정준 축(리서치 7)·vol 태그는
   `:vol:x:` 문자열 규약(리서치 9)·conditions·claim 열이 이미 존재(리서치 10).
   설계: claim→vol 기본값 표(decision→불변 우선·procedure→조건부·lesson→
   조건부·event→불변 감쇠 면제 유지) — **제안만**(`suggested_vol` 확장·
   승인제). 오버라이드는 기존 vol 태그 우선(매핑은 미태깅 행의 기본값).
   감쇠 면제 판정(hot.go:104 EffectiveScoreFor·maintain_recall.go:231)에
   claim=event 면제 추가 — `:vol:immutable:` 태그와 동등.
2. **변동성 출처 축**: SuggestVolClass 시그니처에 source 인자 추가
   (volclass.go:31 — write 경로의 origin 값 전달). 출처별 기본값:
   human/correction→불변 우선·import→조건부·vault→조건부·agent→현행 규칙.
   출처 축도 제안만(자동 태깅 금지·승인제 유지).
3. **자동 분류 적용 경로**: age-audit의 `suggested_vol` 제안(maintain_recall.go:319)
   에 `--apply-vol`(승인제·`--yes` 필수) 적용 경로 신설 — 제안→검수→적용의
   마지막 고리. 적용은 `:vol:x:` 태그 갱신(supersede 아님 — 태그 병합).

## 2. 계약(불변)

P1·P2·K1~K5·비간섭 5원칙·승인제(자동 active 승격 금지)·thresholds 신규값은
등록+meta 오버라이드만·주석 금지·개인 데이터 불침투·기존 vol 태그·immutable
면제 회귀 유지.

## 3. 수용 기준(요지)

1. claim 기본값 표가 미태깅 행의 suggested_vol에 반영·승인 적용 후 회귀에서
   감쇠 면제 정상.
2. event 행이 immutable 면제(`:vol:immutable:` 태그 없이도 claim=event로).
3. SuggestVolClass(source) 오버로드 — 기존 (topic,tags) 호출 전부 무영향.
4. `--apply-vol --yes` 경로: 제안 승인 적용·회귀 테스트로 검증.
5. 스키마 불변·전수 GREEN·release-gate(수리판)+로컬 make ci green.

## 4. 비목표

재확신 처분(v1.22.0 실현)·근원 변화 회로(폐쇄 완성)·자동 태깅(승인제 유지)·
세션 사슬 확장·v1.41 품질 청산 과제.

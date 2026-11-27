# v1.39.0 성장 모델 2단계 기획 — 환류·순위·프로젝트 연결 (2026-11-27)

상태: active

- **선행**: 리서치 완료(2026-11-27 — 12개 즉용 사실·근거 파일:행 수집)·scope3 배분
  (v1.39.0=성장 2단계·스키마 v21)·성장 모델 설계 §8·pipelines §6·§12 승계
- **스코프 3계열로 한정**(v1.40.0 배분 6항목과 중복 금지 — 리서치 §4):
  ①recall 신호→read 랭킹 확장 ②recall-audit 권장 처분(환류 일반화 최소형)
  ③프로젝트 연결(hot 죽은 분기 활성화)

## 1. 판정 — 핵심 결정 3건

1. **프로젝트 연결 축 = work_state.session_id 백필+세션 사슬 해석(스키마 불변 우선)**.
   근거: work_state.session_id·project_id 열이 이미 존재(v20)·hot은 --session 라벨
   수신(리서치 8)·sessions.project_id는 done 기록분에만 의미(세션 중간 프로젝트
   전환 미포착). v21 신규 열은 "세션 사슬로 불충분함이 실증될 때" 보류 —
   additive 판정은 구현 중 G2에서 확정(스키마 버전당 1회 원칙 유지).
   세부: `flint state open`이 세션 라벨을 수신해 session_id 채움·hot은
   현재 세션 라벨→최근 work_state 행의 project_id 해석→project 우선 정렬
   분기(hot.go:420-425) 활성화. 미해석 시 기존 동작(0=무필터·바이트 동일).
2. **recall-audit 권장 처분 필드 신설(decay-audit 패리티)** — recommend:
   supersede|expire|재확신|hold. 근거: decay-audit만 recommend 보유
   (maintain_supersede.go:183)·처분 4경로의 recallAuditDisposed 카운터 연결
   완료(리서치 5)·bad_recall 소비처(recall-active burst) 존재(리서치 6).
   판정 규칙은 코드 상수(비-L·P1 준수 — 무검증 자동 승격 금지)·처분 실행은
   기존 유지 관리 경로 경유(승인제).
3. **read 랭킹 확장은 보수적으로**: read는 read_count를 이미 RRF 신호로 사용
   (리서치 3) — 1단계 신규는 recall_events의 read 경로 확장(횟수 신호·
   RrfK 등록 산식 결합)만, hot 산식 가중은 단일 함수(EventEffectiveScore
   hot.go:80) 유지 원칙으로 hot/brief/decay-audit 3소비자 공유 확인 후 최소
   변경. `:vol:immutable:` 감쇠 면제 계약 유지(리서치 10).

## 2. 계약(불변)

P1·P2·K1~K5·비간섭 5원칙·work_state 수명 계약(open 무조건 기록·resolve·
보존 90일)·claim 게이트·hot 부재 시 바이트 동일·thresholds 신규값은
등록+meta 오버라이드만·개인 데이터 repo 불침투·주석 금지·TDD.

## 3. 수용 기준(요지)

1. recall 신호가 read 순위에 반영되고 제로히트·회귀 없음(전수 GREEN).
2. recall-audit 출력에 recommend 필드·처분 카운터 전환율(D4) 계량 가능.
3. 세션 라벨로 project 해석 성공 시 hot 스냅샷이 프로젝트 우선 정렬·
   미해석 시 바이트 동일(1단계 회귀 유지).
4. 스키마 변경은 additive만(불변 우선·v21 필요성 실증 시 G2 확정).
5. 문서=코드 동일 게이트(doclint·release-gate·로컬 make ci 패리티) green.

## 4. 비목표

sessions.project_id 신설(축 판정 보류)·자동 처분 실행(승인제 유지)·
v1.40.0 배분 6항목(재확신·변동성 출처·주제 분류·근원 변화·claim 매핑·
재점검 큐 확장)·임베딩(ADR-0011)·원격 쓰기(ADR-0015).

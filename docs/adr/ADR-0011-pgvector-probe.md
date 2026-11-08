# ADR-0011: Doltgres pgvector 에뮬 실증 probe (v0.25.0)

- 형식: ADR (조사 기록 — 채택 아님)
- 날짜: 2026-11-08
- 상태: **probe 완료 — 채택 보류**(트리거: zero_pct 20%+read_calls 50)

## 배경

PLAN §15의 pgvector 보류 사유는 "Doltgres 에뮬레이션 실증 전"이었다.
Doltgres가 2026-09-03 확장 에뮬레이션 발표(dolthub.com/blog/2026-09-03-doltgres-extensions-update)로 vector 확장이 구현됐다는
발표가 있어 합성 데이터 probe로 실증했다.

## probe 결과 (flint_test DB·합성 데이터)

- `CREATE EXTENSION vector` → **성공**(extversion 0.8.6)
- `vector(3)` 컬럼 테이블 생성·INSERT → 성공
- cosine 거리 연산자 `<=>` ORDER BY → **성공**(기대 상위 반환)

## 판정

에뮬레이션이 **기본 CRUD·cosine 순위 쿼리에서 동작함을 확인**했다.
채택은 보류한다 — 임계 요인이 에뮬 자체가 아니라:

1. **임베딩 생성 경로**: 외부 API(OpenAI 등)는 개인 데이터 외부
   전송(저장소 최상위 원칙 위반), 로컬 모델은 바이너리 수십 MB
   (단일 바이너리 원칙 부담).
2. **성능·인덱스**: HNSW/IVFFlat 인덱스가 에뮬에서 동작하는지,
   만 행 규모 지연 프로파일은 미측정(수요 전 측정 불필요).
3. **수요 트리거 미도달**: zero_pct 20%+read_calls 50 도달 시
   재검토 — 앱 사이드 RRF(v0.25.0 R1)가 먼저 그 갭을 줄인다.

트리거 도달 시 이 ADR을 갱신해 채택/기각을 판정한다.

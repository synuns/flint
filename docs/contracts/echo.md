# echo 위계 계약 — knowledge write 흐름 (v1.36.0 고정)

## 범위

`lib/knowledge/write.go`의 knowledge 분기 실물 흐름을 그대로 문서화한다.
본 문서는 동작을 새로 정의하지 않는다 — 코드와 불일치하면 문서가 아니라
드리프트이며, `internal/doclint/echo_contract_test.go`의
`TestEchoContractFrozen`이 red로 고정한다. 동작 변경은 본 계약의 개정
(spec 합의) 없이 금지.

## 실물 흐름 순서

| 단계 | 동작 | write.go 소스 마커 |
|---|---|---|
| 1 | 배치 seen-dup — 같은 배치 내 중복 페이로드 스킵 | `if r.seen[h] {` |
| 2 | content_hash 기존 행 조회 | `existsMeta, exists, err := lookupHash(r.ctx, r.st, h)` |
| 3 | echo 어드바이저리 스캔 — duplicates/contradicts 보고 (기존 행 존재 시 단일 페이로드 `r.dupEcho`, 신규 분기 `r.echoCands`) | `scanEcho(r.ctx, r.st, n, r.th)` |
| 3b | 신규 분기 echo 스캔 — 기존 행이 없을 때 Classify 직전에 스캔 고정 | `r.echoCands = scanEcho(r.ctx, r.st, n, r.th)` |
| 4 | 상태 분류 — confidence 기반 pending/active | `store.Classify(r.kind, conf, r.floor)` |
| 5 | 중복 NOOP 출력 — persist 단계에서 기존 행만 남으면 전체 배치는 emitDuplicate로 귀결 | `func (r *writeState) emitDuplicate()` |
| 6 | 신규 추가 — InsertBatch | `InsertBatch(r.ctx, r.recs)` |

echo 스캔은 어드바이저리다 — 스캔 결과(duplicates/contradicts)는 보고 전용이며,
NOOP·삭제·갱신 판정을 자동 수행하지 않는다. 기존 행과 정확히 일치하면
NOOP(emitDuplicate)로 귀결되고 echo 후보가 중복 출력에 동봉된다.

## 위계와 위임 어휘

위계: **NOOP > 삭제 > 갱신 > 추가** — flint write는 위계 최상위(NOOP)만
스스로 수행하고, 나머지는 위임 대상 언어(영역 B 계약 어휘)로 유도한다.

- NOOP=emitDuplicate — content_hash 일치 재송신. 삭제·갱신 없이 기존 행
  유지, 어드바이저리 echo 동봉.
- 삭제=maintain/invalidate — write가 직접 삭제하지 않는다. invalidation은
  `maintain` 경로(invalidate)에 위임.
- 갱신=approve/pending — 상태 갱신·승격은 `approve` 경로(supersede)와
  pending 승격 제안에 위임. write는 Classify의 초기 상태만 부여.
- 추가=Classify 통과 — 기존 행이 없을 때만 `store.Classify` 결과
  (pending/active)로 신규 추가.

## 경계 케이스 표

| 항목 | 판정 조건 | 기본값 | 근거 소스 |
|---|---|---|---|
| duplicates 임계 | multiset bigram dice ≥ ConflictDupThreshold | 0.6 | lib/store/thresholds.go |
| contradicts 임계 | BigramDice(전체) ≥ EchoMinScore | 0.3 | lib/knowledge/echo.go |
| topic 일치 | BigramDice(topic, 행 topic) ≥ EchoTopicMatch | 0.4 | lib/knowledge/echo.go |
| 스캔 상한 | SuggestScanCap 행수 LIMIT (invalid_at IS NULL) | 5000 | lib/store/thresholds.go |
| 보고 상한 | EchoTopK | 5 | lib/knowledge/echo.go |
| 입력 절단 | topic+summary 초과분 절단 (echoInputMaxRunes) | 4096 | lib/knowledge/echo.go |
| reason(relation) 필드 | 2분기 고정 — `"duplicates"` / `"contradicts"` | — | lib/knowledge/echo.go EchoRelation* |

reason 필드는 2분기(duplicates·contradicts)만 존재한다. 가상의 3·4분기
분기를 추가하는 것은 동작 변경이며 본 계약 위반 — 임계값 상향·하향 조정은
코드 기본값 + meta 오버라이드(thresholds 관례)로만 한다.

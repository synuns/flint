# flint 임계값 튜닝 가이드

flint의 임계값·예산은 하드코딩 금지 원칙 아래 meta 테이블로 오버라이드한다.
기본값은 코드(`lib/store/thresholds.go`)에 있고, meta 키가 우선한다.
잘못된 값은 경고와 함께 기본값으로 폴백한다(에러 아님).

## 1. hot 예산(주입 상한) 튜닝

절차(§15 이연결정 "주입 예산 상한: hot_view 2주 실측 p95로 확정" 회수):

1. **관측(2주)** — 세션마다 `flint status`의 `hot_calls`·`hot_p95`를 기록한다
   (v0.3 계측 — 프로세스 내 통계, 관측 세션 단위).
2. **예산 갱신** — 현재 hot 상한은 `hot_budget` 메타 키로 확정한다(v0.4부터
   `flint hot`이 매 실행 이 키를 읽는다 — 미설정·무효 값은 기본 2,000):
   ```sh
   psql "$FLINT_DSN" -c "INSERT INTO meta (key, value) VALUES ('hot_budget','2400')
     ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value"
   ```
3. **반영 확인** — `flint hot --session <id>` 출력이 새 상한에서 절단되는지,
   `flint status`의 `hot_p95`가 상한 아래로 유지되는지 확인한다.

## 2. 임계값 오버라이드 (§15 이연결정 "임계값 수치 운영 중 조정" 회수)

| meta 키 | 기본값 | 의미 | 경고 발동 |
|---|---|---|---|
| `pending_max` | 50 | 미기록(pending) 결정 상한 | maintain·done |
| `dup_susp_max` | 20 | 중복의심 결정 상한 | maintain |
| `conf_floor` | 0.8 | 확신 하한(0~1) | maintain |
| `stale_sessions` | 3 | 연속 미기록 세션 수 | done |

갱신(미설정 키는 기본값, 즉시 재적용 — 재시작 불필요):

```sh
psql "$FLINT_DSN" -c "INSERT INTO meta (key, value) VALUES ('pending_max','40')
  ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value"
```

확인: `flint maintain` 보고의 Counts vs Thresholds 비교가 새 값을 따르는지,
`flint done --session <id>` 경고 기준이 바뀌는지.

## 3. 실측 예시 (가상 수치)

| 주차 | hot_calls | hot_p95 | 조치 |
|---|---|---|---|
| 1주차 | 38 | 1,780자 | 관측만 |
| 2주차 | 41 | 1,960자 | 상한 2,000자 근접 — `hot_budget` 2,400 갱신 |
| 갱신 후 | 12 | 2,310자 | 절단 0건 — 예산 적정 |

## 4. 기타 meta 키

- `schema_version` — CLI 호환 검사 전용(수동 갱신 금지).
- `last_backup` — 자동 백업 판정 전용(`flint backup`이 갱신).
- `hot_stats` — hot 계측 영속 누적(`flint status`가 읽고 종료 시 갱신).

## 5. 이연결정 회수 계획 요약

§15 이연 4건 중 이 문서가 회수하는 것은 ①주입 예산 상한(hot_budget)과
④임계값 수치다. ②보존·영구삭제·③백업 포맷은 데이터 축적·pg_dump 검증
이후 별도 결정한다.

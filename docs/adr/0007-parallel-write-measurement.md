# ADR-0007: 병렬 쓰기 실측 — 8 동시 writer 전원 커밋

- **상태**: 채택 (2026-10-22, T50 실측)
- **맥락**: v0.5 신뢰성 목표 — 다중 에이전트가 `flint write`를 동시에 실행해도
  손실·중복 없이 전원 커밋되어야 한다. 커밋 경로는 `dolt_add`+`dolt_commit`
  에 `Retry`(0s/1s/3s)를 얹은 구조(lib/db/repo.go).
- **측정 방법**: `TestParallel8WritersAllCommitted`(tests/e2e/parallel_test.go) —
  8 goroutine이 서로 다른 payload로 `bin/flint write knowledge` 실 프로세스를
  동시 실행. 재시도 경유 판정은 writer별 경과 ≥0.9s(첫 재시도 지연 1s)를
  근사 프록시로 사용.
- **결과(1회 실행 측정값)**: N=8, exit 0 8건 / exit 1 0건, committed 8 /
  duplicate 0, knowledge +8. content_hash 출력-DB 8:8 정합(손실·중복 0).
  충돌 발생률 0%(0/8), 재시도 소요 0ms(재시도 발생 없음, 전원 1차 시도
  커밋). wall-clock 273ms, 최대 writer 경과 273ms.
- **결정**: 병렬 쓰기 정합은 content_hash UNIQUE 제약 + Retry 폴백으로
  현재 구조를 유지한다. 충돌이 실제 발생하는 회귀 시각은
  `TestRetryRecoversAfterTransientCommitFailure`(재시도 1회 ≥1s 경유)가
  별도로 담당하므로 이 실측은 무충돌 경로의 회귀 게이트로 둔다.
- **되돌림 트리거**: 동시성을 늘리거나(N>8) 충돌 빈발 환경에서 손실·중복이
  관측되면 격리 수준·커밋 직렬화 전략 재평가.

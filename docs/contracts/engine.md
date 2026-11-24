# Engine & 재진입 계약

Sources: `lib/engine/download.go` · `lib/mcp/tools.go` · ADR-0021.

## mcp → cli 재진입

MCP 도구(`flint mcp`, stdio)는 CLI 명령 파이프라인을 우회하지 않고
**재진입**한다.

- 진입점: `lib/mcp/tools.go`의 `runFn` → `runWithStoreFn`
  (= `cli.RunWithStore`) · 스토어 공급은 `storeSupply`
  (= `cli.OpenServerStore`).
- 스토어는 프로세스당 1회 열고 캐시한다(`serverStore`). 도구 호출
  간 재연결하지 않는다.
- 도구 출력은 해당 CLI 명령의 출력 그대로이며, exit code 계약도
  CLI와 동일하게 매핑된다. CLI 명령 표면(인자·출력·종료 코드) 변경은
  MCP 계약 변경이다.
- 재진입 경로의 seam은 `docs/contracts/seam.md` 규칙의 명목 예외로
  등록된 기존 var func seam이다(점진 축소 대상).

## 엔진 다운로드 방어 규칙

`fetchTarball`은 다운로드 실패(io.Copy 오류)시 대상 파일(dst)을
즉시 삭제한다 — 부분 기록 잔존 금지. 상위 호출자의 임시 디렉터리
정리에 의존하지 않고 자체 완결성을 유지한다.

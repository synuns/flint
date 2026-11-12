# MCP 계약 — `flint mcp` (도구 5종·로컬 stdio)

원천: `lib/mcp/tools.go`·`lib/mcp/serve.go`·ADR-0015.

## 계약

- 전송은 **stdio**다. 클라이언트가 같은 기기에서 `flint mcp` 프로세스를
  spawn한다(JSON-RPC: initialize → tools/list → tools/call, EOF 정상
  종료 0).
- 도구 5종(`ToolsManifest`, v0.32.0):

| 도구 | 동작 |
|---|---|
| `flint_read` | 활성 knowledge 검색(query/tag 필터, limit 상한 50) |
| `flint_write` | kind(knowledge/session/project)+payload 기록, 동일 payload는 duplicate(멱등) |
| `flint_hot` | 주입용 핫 요약(2,000자 상한, session_id 선택) |
| `flint_pending` | 대기 지식 목록 + 규칙 기반 추천(verdict: promote/merge/expire/hold) |
| `flint_approve` | pending 승격(선택 supersede — 구 활성 지식 대체 병합) |

- 도구 호출은 cli.Run 인자를 조립해 실행한다 — CLI 출력 문자열과
  exit 코드(0/1/2/3)를 그대로 반환(실DB·계약 재사용, 별도 구현 없음).
- 서버 버전은 `cli.VersionString`(ldflags 주입)을 초기화 응답에 녹인다.

## 경계

- 외부 입력 query는 플래그로 파싱되지 않게 `--` 뒤에 둔다
  (end-of-options — 인젝션 방지).
- 미정의 도구는 `{"error":"unknown tool: <name>"}`, exit 1(이름 이스케이프).

## 금지

- 도구 5종 외 표면 추가 시 manifest와 이 문서 동시 갱신 없이
  배포하지 않는다(SKILL 도구 목록 가드가 대조한다).
- 원격(비로컬) 클라이언트 지원 표방 — stdio 전제 위반(bridge.md 참조).

## 예시

```json
{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"flint_read","arguments":{"query":"인덱스 전략","limit":10}}}
```

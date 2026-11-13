# 레지스트리 계약 — mcp-registry 발행·서명

원천: `mcp-registry/server.json.template`·`scripts/publish_mcp.sh`·ADR-0014.

## 계약

- 발행물: MCP Registry 스키마 매니페스트(name
  `io.github.synuns/flint`·description·package) — 발행 원천은
  **template + CI 렌더**이며 렌더 결과물 server.json은 repo에
  없다(v1.2.3 잔재 삭제).
- 발행 경로: 태그 push 시 CI가 `scripts/publish_mcp.sh`를 실행한다
  (로컬 수동 실행 가능 — mcp-publisher login 후). 스크립트는
  dist/의 `.mcpb`에서 SHA256을 계산해 template의 `{{URL}}`·
  `{{SHA256}}`·`{{VERSION}}`을 치환·렌더하고 `mcp-publisher publish`로
  발행한다. URL은 GitHub 릴리스 자산을 가리킨다.
- 전송 선언: stdio (`command: flint`, `args: ["mcp"]`) — mcp.md의
  로컬 전제와 동일.
- **서명(ADR-0014)**: CI가 릴리스 시 checksums.txt에
  `cosign sign-blob`(tlog-upload=false)으로 서명해 `.sig` 자산으로
  올린다. `flint update`는 임베드 공개키(`lib/cli/cosign.pub`)로
  **체크섬 대조보다 먼저** 서명을 검증한다 — 계정 침해자는 위조
  체크섬을 올려도 서명은 만들지 못한다.

## 경계

- 전환 모드: 시크릿 미설정·공개키 미임베드 시 서명을 스킵/경고 후
  진행(하드 검증은 키 등록 후).

## 금지

- server.json을 template 없이 직접 편집 — 다음 렌더에서 덮어써진다.
- 서명 없이 검증 우회하는 기본 경로 제공
  (`--insecure-skip-signature`는 키 롤링 이전용 명시적 플래그).

## 예시

```sh
./scripts/publish_mcp.sh   # 태그 인자 생략 시 git describe로 최신 태그 감지
# dist/ .mcpb SHA256 → template 렌더 → mcp-publisher publish
```

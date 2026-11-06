#!/bin/bash
# publish_mcp.sh — MCP Registry 발행 (태그 push 시 CI에서 자동 실행,
# 로컬에서도 수동 실행 가능).
#
# CI (release job): mcp-publisher login github-oidc 후 이 스크립트 호출.
# 로컬: mcp-publisher login github 후 ./scripts/publish_mcp.sh [tag]
#
# 인자: 태그명(기본: git describe --tags --abbrev=0). dist/의 .mcpb에서
# SHA256 계산 → template 렌더 → mcp-publisher publish.
set -euo pipefail
cd "$(dirname "$0")/.."

TAG="${1:-$(git describe --tags --abbrev=0)}"
VER="${TAG#v}"
# snapshot이면 -SNAPSHOT-<sha> 접미사가 붙으므로 glob으로 탐색.
MCPB=$(ls dist/flint_*_darwin_arm64.mcpb 2>/dev/null | head -1 || true)
if [ -z "$MCPB" ]; then
  MCPB="dist/flint_${VER}_darwin_arm64.mcpb"
fi
TEMPLATE="mcp-registry/server.json.template"
RENDERED="/tmp/flint-server.json"

if [ ! -f "$MCPB" ]; then
  echo "error: $MCPB 없음 — goreleaser 실행 후 재시도" >&2
  exit 1
fi

SHA256=$(shasum -a 256 "$MCPB" | cut -d' ' -f1)
URL="https://github.com/synuns/flint/releases/download/${TAG}/flint_${VER}_darwin_arm64.mcpb"

sed -e "s|{{URL}}|${URL}|g" \
    -e "s|{{SHA256}}|${SHA256}|g" \
    -e "s|{{VERSION}}|${VER}|g" \
    "$TEMPLATE" > "$RENDERED"

echo "렌더: ${RENDERED} (URL=${URL}, SHA=${SHA256:0:12}…)"

if command -v mcp-publisher >/dev/null 2>&1; then
  mcp-publisher publish "$RENDERED"
  echo "발행 완료: ${TAG}"
else
  echo "mcp-publisher 미설치 — 렌더된 파일만 생성: ${RENDERED}"
  echo "설치: brew install mcp-publisher"
fi

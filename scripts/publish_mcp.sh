#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."

TAG="${1:-$(git describe --tags --abbrev=0)}"
VER="${TAG#v}"
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

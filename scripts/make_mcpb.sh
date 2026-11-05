#!/usr/bin/env bash
# make_mcpb.sh — flint MCP 번들(.mcpb) 생성 (v0.16.5 T2).
# .mcpb = zip(manifest.json + flint 바이너리) — MCP Registry 배포 단위.
#
# 사용: make_mcpb.sh <os>_<arch> [version] [binary]
#   <os>_<arch>  필수(예: darwin_arm64 — goreleaser .Target과 동일 형식)
#   version      생략 시 GORELEASER_CURRENT_TAG(v 제거) → git 태그 fallback
#   binary       생략 시 dist/flint_<os>_<arch>/flint (goreleaser 기본 산출 경로)
# 산출: dist/flint_<version>_<os>_<arch>.mcpb
#
# goreleaser post hook에서 호출: ./scripts/make_mcpb.sh {{ .Target }} {{ .Version }} {{ .Path }}
# Release 자산화는 .goreleaser.yml release.extra_files(dist/*.mcpb glob).
set -euo pipefail

usage() {
  sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

target="${1:?usage: make_mcpb.sh <os>_<arch> [version] [binary]}"
# goreleaser .Target는 접미사 포함(예: darwin_arm64_v8.0) — 아카이브명과
# 동일한 <os>_<arch>로 정규화(_v* 제거).
target="${target%%_v*}"
root="$(cd "$(dirname "$0")/.." && pwd)"

version="${2:-}"
if [[ -z "$version" && -n "${GORELEASER_CURRENT_TAG:-}" ]]; then
  version="${GORELEASER_CURRENT_TAG#v}"
fi
if [[ -z "$version" ]]; then
  version="$(git -C "$root" describe --tags --abbrev=0 | sed 's/^v//')"
fi

binary="${3:-$root/dist/flint_${1}/flint}"
out_dir="$root/dist"
out="${out_dir}/flint_${version}_${target}.mcpb"

if [[ ! -f "$binary" ]]; then
  echo "make_mcpb: 바이너리 없음: $binary" >&2
  exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

cat > "$work/manifest.json" <<EOF
{"name":"flint","version":"${version}","description":"Agent long-term memory — search, record, inject","tools":["flint_read","flint_write","flint_hot"],"transport":"stdio","command":"flint","args":["mcp"]}
EOF

cp "$binary" "$work/flint"
# -j: zip 루트에 평면 배치(하위 디렉터리 제거) — 매니페스트·바이너리만.
(cd "$work" && zip -q -j "$out" manifest.json flint)

echo "$out"

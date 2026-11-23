#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."

if [ $# -ne 1 ]; then
  echo "usage: $0 <version>  (예: 0.17.1)" >&2
  exit 1
fi
NEW="$1"
if ! [[ "$NEW" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "error: 버전 형식은 x.y.z (예: 0.17.1) — 입력: $NEW" >&2
  exit 1
fi

OLD=$(cat VERSION)
if [ "$OLD" = "$NEW" ]; then
  echo "이미 VERSION=$NEW — 변경 없음"
  exit 0
fi

echo "$NEW" > VERSION

for f in README.md README.ko.md; do
  perl -pi -e "s{release-v[0-9]+(?:\.[0-9]+)+-blue}{release-v${NEW}-blue}g" "$f"
  perl -ni -e 'if (/release-v/) { s{alt="v[0-9]+(?:\.[0-9]+)+"}{alt="v'"${NEW}"'"}g } print' "$f"
done

perl -pi -e "s{\"version\": \"[0-9]+(?:\.[0-9]+)+\"}{\"version\": \"${NEW}\"}" \
  flint-plugin/.claude-plugin/plugin.json

echo "VERSION: $OLD -> $NEW"
echo
git diff --stat

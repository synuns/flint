#!/bin/bash
# bump_version.sh — 버전 일괄 범프 (배지 누락 사고 3회 방지, v0.17.0 R1).
#
# 사용법: ./scripts/bump_version.sh <version>   (예: 0.17.1)
#
# 갱신 대상(단일 버전 소스 동기):
#   1. VERSION                  — 단일 버전 소스(v0.16.7 autotag 잡이 감지)
#   2. README.md·README.ko.md   — release-v<x.y.z> 배지
#   3. flint-plugin/.claude-plugin/plugin.json — version 필드
# 마지막에 git diff --stat으로 변경 요약 표시.
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
  # 배지 URL의 release-v<x.y.z>만 교체(alt 텍스트 등 다른 v 표기는 불간섭).
  perl -pi -e "s{release-v[0-9]+(?:\.[0-9]+)+-blue}{release-v${NEW}-blue}g" "$f"
done

perl -pi -e "s{\"version\": \"[0-9]+(?:\.[0-9]+)+\"}{\"version\": \"${NEW}\"}" \
  flint-plugin/.claude-plugin/plugin.json

echo "VERSION: $OLD -> $NEW"
echo
git diff --stat

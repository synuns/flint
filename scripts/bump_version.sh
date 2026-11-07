#!/bin/bash
# bump_version.sh — 버전 일괄 범프 (배지 누락 사고 3회 방지, v0.17.0 R1).
#
# 사용법: ./scripts/bump_version.sh <version>   (예: 0.17.1)
#
# 갱신 대상(단일 버전 소스 동기):
#   1. VERSION                  — 단일 버전 소스(v0.16.7 autotag 잡이 감지)
#   2. README.md·README.ko.md   — release-v<x.y.z> 배지·인용 하이라이트 버전
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
  # 배지 URL의 release-v<x.y.z>과 같은 줄의 alt="v<x.y.z>"를 함께 교체
  # (v0.18.1 — alt가 구버전으로 남아 스크린리더가 잘못 읽던 갭).
  perl -pi -e "s{release-v[0-9]+(?:\.[0-9]+)+-blue}{release-v${NEW}-blue}g" "$f"
  perl -ni -e 'if (/release-v/) { s{alt="v[0-9]+(?:\.[0-9]+)+"}{alt="v'"${NEW}"'"}g } print' "$f"
  # v0.20.3(패치 루프 3차 D2): 인용 하이라이트 줄의 버전 접두도 동기 —
  # 배지와 달리 이 줄은 게이트 밖이라 스크립트가 책임진다.
  perl -pi -e "s{^> v[0-9]+(?:\.[0-9]+)+ ·}{> v${NEW} ·}" "$f"
done

perl -pi -e "s{\"version\": \"[0-9]+(?:\.[0-9]+)+\"}{\"version\": \"${NEW}\"}" \
  flint-plugin/.claude-plugin/plugin.json

echo "VERSION: $OLD -> $NEW"
echo
git diff --stat

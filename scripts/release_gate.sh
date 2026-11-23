#!/bin/bash
# release_gate.sh — 태그 전 릴리스 메타 일관 게이트.
# 이 루프에서 버전 누락 사고 4회(v1.4.1·v1.4.2·v1.5.1·v1.7.1 — HISTORY/계약행 부재)의
# 재발 방지. 릴리스 커밋은 이 게이트를 통과해야 태그할 수 있다.
# 검사: VERSION == plugin.json == README EN/KO 배지 == HISTORY 항목 == versions_test 계약행.
set -euo pipefail
cd "$(dirname "$0")/.."

ver="$(cat VERSION)"
fail() { echo "release-gate FAIL: $1 (version=${ver})" >&2; exit 1; }

# 1. plugin.json
pj="$(python3 -c 'import json;print(json.load(open("flint-plugin/.claude-plugin/plugin.json"))["version"])')"
[ "$pj" = "$ver" ] || fail "plugin.json=${pj}"

# 2. README 배지 양측
grep -q "release-v${ver}-blue" README.md || fail "README.md 배지 부재"
grep -q "release-v${ver}-blue" README.ko.md || fail "README.ko.md 배지 부재"

# 3. HISTORY 항목 (v1.x.y 형식 헤더)
grep -qE "^### v${ver//./\\.}" docs/HISTORY.md || fail "HISTORY 항목 부재 (릴리스 관례 위반 — v1.4.1/v1.4.2/v1.5.1/v1.7.1 사고 재발)"

# 4. versions_test 계약행 (TestVersion0XYMilestone)
major_minor_patch="${ver#v}" 2>/dev/null || true
nums="0${ver//./}"
row="TestVersion${nums}Milestone"
grep -q "$row" internal/doclint/versions_test.go || fail "versions_test 계약행(${row}) 부재"

# 5. docs 계약 green (배지-하이라이트-버전 동기 테스트 포함)
make docs-sync >/dev/null || fail "make docs-sync red"

# 6. 하이라이트 라벨 유일 (v1.26.2 r113-1 — 범프 스크립트가 37줄 전량을
# 동일 라벨로 왜곡한 사고의 재발 방지). 전 블록 단조는 역사상 부정확
# (v0.44.0 재무장 이후 아카이브 구간은 오름차순)이라 유일성만 검사한다.
for rf in README.md README.ko.md; do
  python3 - "$rf" <<'PYEOF' || fail "README 하이라이트 라벨 유일 위반 (${rf})"
import re, sys
labels = re.findall(r"^> v(\d+(?:\.\d+)+) ·", open(sys.argv[1]).read(), re.M)
if len(set(labels)) != len(labels):
    sys.exit(1)
PYEOF
done

echo "release-gate OK: v${ver} — VERSION·plugin.json·배지(EN/KO)·HISTORY·계약행·docs-sync·하이라이트"

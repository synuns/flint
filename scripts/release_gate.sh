#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."

ver="$(cat VERSION)"
fail() { echo "release-gate FAIL: $1 (version=${ver})" >&2; exit 1; }

fmt_out="$(gofmt -l lib internal tests cmd)"
[ -z "$fmt_out" ] || { echo "release-gate FAIL: gofmt 미적용 파일 (version=${ver})" >&2; echo "$fmt_out"; exit 1; }
echo "release-gate: gofmt 스윕 OK (lib internal tests cmd)"

python3 scripts/check_comments.py || fail "scripts/check_comments.py red"

engine_port="$(sed -n 's/.*127\.0\.0\.1:\([0-9]*\)\/.*/\1/p' internal/testengine/dsn.go | head -1)"
[ -n "$engine_port" ] || engine_port=5499
if (echo > "/dev/tcp/127.0.0.1/${engine_port}") 2>/dev/null; then
  FLINT_REALDB_REQUIRED=1 make e2e || fail "make e2e red"
  echo "release-gate: make e2e OK"
else
  echo "e2e skipped (engine down)"
fi

pj="$(python3 -c 'import json;print(json.load(open("flint-plugin/.claude-plugin/plugin.json"))["version"])')"
[ "$pj" = "$ver" ] || fail "plugin.json=${pj}"

grep -q "release-v${ver}-blue" README.md || fail "README.md 배지 부재"
grep -q "release-v${ver}-blue" README.ko.md || fail "README.ko.md 배지 부재"

grep -qE "^### v${ver//./\\.}" docs/HISTORY.md || fail "HISTORY 항목 부재 (릴리스 관례 위반 — v1.4.1/v1.4.2/v1.5.1/v1.7.1 사고 재발)"

major_minor_patch="${ver#v}" 2>/dev/null || true
nums="0${ver//./}"
row="TestVersion${nums}Milestone"
grep -q "$row" internal/doclint/versions_test.go || fail "versions_test 계약행(${row}) 부재"

make docs-sync >/dev/null || fail "make docs-sync red"

for rf in README.md README.ko.md; do
  python3 - "$rf" <<'PYEOF' || fail "README 하이라이트 라벨 유일 위반 (${rf})"
import re, sys
labels = re.findall(r"^> v(\d+(?:\.\d+)+) ·", open(sys.argv[1]).read(), re.M)
if len(set(labels)) != len(labels):
    sys.exit(1)
PYEOF
done

echo "release-gate OK: v${ver} — VERSION·plugin.json·배지(EN/KO)·HISTORY·계약행·docs-sync·하이라이트·gofmt·commentlint·e2e"

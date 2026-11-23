#!/bin/sh
set -u
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VERDICT=$(python3 - "$ROOT" <<'PYEOF'
import json, re, sys, os
root = sys.argv[1]
try:
    d = json.load(sys.stdin)
    cmd = (d.get("tool_input") or {}).get("command", "") or ""
except Exception:
    print("BLOCK json 파싱 실패"); sys.exit(0)

GIT_STATE = re.compile(
    r"\bgit\b(?:\s+(?:-[A-Za-z-]+|--[A-Za-z-]+)(?:\s+(?:\"[^\"]*\"|'[^']*'|\S+))?)*"
    r"\s+(commit|push|add|cherry-pick|merge|update-index|restore|stage|stash|rebase|am|apply)\b"
)
if GIT_STATE.search(cmd):
    if "--author" in cmd:
        print("BLOCK --author 주입 금지 — 커밋 작성자는 로컬 git config(noreply)로 고정.")
        sys.exit(0)
    print("GUARD git 상태변경 감지 — check_personal 실행")
    sys.exit(0)
print("PASS 비 git 상태변경")
PYEOF
)
case "$VERDICT" in
  BLOCK*)
    echo "$VERDICT" | sed 's/^BLOCK //' >&2
    exit 2 ;;
  GUARD*)
    if ! python3 "$ROOT/scripts/check_personal.py" --cached >/dev/null 2>&1; then
      echo "BLOCK: 개인 데이터 마커 감지 — scripts/check_personal.py 기준 위반. 치환 후 재시도." >&2
      exit 2
    fi ;;
esac
exit 0

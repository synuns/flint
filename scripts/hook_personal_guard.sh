#!/bin/sh
# hook_personal_guard.sh — 하네스 PreToolUse(Bash) 가드.
# 판정 전체를 python3(저장소 툴체인 필수)에서 수행 — 셸 grep 방언(ugrep
# 등) 무관·결정적. 실패는 닫힘(fail-closed): 파싱/판정 불가 시 차단.
# 검출 계층(F1/F4 우회 차단): 접두·체이닝·경로·플래그 무관하게 git
# 상태변경 토큰을 잡고 --author 주입을 금지한 뒤 check_personal 검사.
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

# git 상태변경 감지 — git (+ 반복 가능한 플래그/인자) + 대상 서브커맨드.
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

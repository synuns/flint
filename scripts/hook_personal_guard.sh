#!/bin/sh
# hook_personal_guard.sh — 하네스 PreToolUse(Bash) 가드.
# git 커밋/푸시/스테이지 직전에 개인 데이터 마커를 검사해 차단(exit 2).
# 기준·마커는 scripts/check_personal.py와 동일(단일 소스).
CMD=$(jq -r '.tool_input.command // empty')
case "$CMD" in
  git\ commit*|git\ push*|git\ add*|git\ cherry-pick*|git\ merge*)
    if ! python3 "$(dirname "$0")/check_personal.py" >/dev/null 2>&1; then
      echo "BLOCK: 개인 데이터 마커 감지 — scripts/check_personal.py 기준 위반. 치환 후 재시도." >&2
      exit 2
    fi
    ;;
esac
exit 0

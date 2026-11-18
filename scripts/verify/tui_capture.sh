#!/bin/bash
# tui_capture.sh — flint TUI를 tmux 세션에서 구동해 텍스트 스냅샷을 남긴다.
# 사용법:
#   scripts/verify/tui_capture.sh --cmd './bin/flint export --tui' \
#       --keys 'tab,enter' --out .superlite/verify/vX/capture.txt
# 인자:
#   --cmd  <인용된 커맨드>       구동할 TUI 커맨드(FLINT_DSN 포함해 전달)
#   --keys <쉼표 키 목록>        tmux 키 이름 순서대로 주입(기본: 없음)
#   --out  <파일>                capture-pane 결과 경로(기본: stdout)
#   --wait <초>                  구동 후 안정 대기(기본 1)
# 세션은 이 스크립트가 만든 것만 종료한다(프로세스명 킬 금지).
set -euo pipefail

cmd="" keys="" out="" wait=1
while [ $# -gt 0 ]; do
  case "$1" in
    --cmd)  cmd="$2"; shift 2 ;;
    --keys) keys="$2"; shift 2 ;;
    --out)  out="$2"; shift 2 ;;
    --wait) wait="$2"; shift 2 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done
[ -n "$cmd" ] || { echo "--cmd required" >&2; exit 2; }

session="flint-verify-$$"
tmux new-session -d -x 200 -y 50 -s "$session" "$cmd"
trap 'tmux kill-session -t "$session" 2>/dev/null || true' EXIT
sleep "$wait"

if [ -n "$keys" ]; then
  IFS=',' read -ra keylist <<< "$keys"
  for k in "${keylist[@]}"; do
    tmux send-keys -t "$session" "$k"
    sleep 0.4
  done
  sleep "$wait"
fi

if [ -n "$out" ]; then
  mkdir -p "$(dirname "$out")"
  tmux capture-pane -p -t "$session" > "$out"
  echo "captured: $out"
else
  tmux capture-pane -p -t "$session"
fi

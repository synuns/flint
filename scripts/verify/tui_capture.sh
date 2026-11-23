#!/bin/bash
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
tmux set-option -t "$session" remain-on-exit on
trap 'tmux kill-session -t "$session" 2>/dev/null || true' EXIT

wait_for_pane() {
  for _ in $(seq 1 50); do
    [ -n "$(tmux capture-pane -p -t "$session" | tr -d '[:space:]')" ] && return 0
    sleep 0.1
  done
  return 0
}
wait_for_pane
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

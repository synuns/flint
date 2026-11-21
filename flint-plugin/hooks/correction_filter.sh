#!/usr/bin/env bash
# correction_filter.sh — UserPromptSubmit hook (v1.14.0 T2, plan A):
# scan the submitted prompt for correction markers and accrue the first
# matched fragment (marker +/- 80 codepoints of context) as a low-trust
# knowledge row via `flint write knowledge --source correction` at an
# explicit confidence 0.4 (below conf_floor, so it always lands pending
# for review — the same gate as the B-plan default 0.7 path).
# Non-interference contract: a non-matching prompt never calls flint
# (zero DB writes); any failure (kill switch, bad stdin, no flint, flint
# error) exits 0 silently — the hook must never block a prompt.
set -u

# Kill switch (contract 3): off means exit before any parsing or call.
[ "${FLINT_CORRECTION_HOOK:-}" = "off" ] && exit 0

# Marker regex (the plan minimal set): Korean 그게 아니라/틀렸어/아니야/다시,
# English actually/no,/that's wrong/correction:. The (^|[^A-Za-z]) prefix
# on the English words guards against infix hits (e.g. "eventually");
# "that.s" stands for the apostrophe so this single-quoted program stays
# quote-free. First match wins; case-insensitive for the Latin set.
regex='그게 *아니라|틀렸어|아니야|다시|(^|[^A-Za-z])actually|(^|[^A-Za-z])no,|that.s wrong|correction:'

# Hook stdin is the UserPromptSubmit JSON; the prompt rides .prompt.
prompt=$(jq -r '.prompt // empty' 2>/dev/null)
[ -z "$prompt" ] && exit 0

# snippet — the first match plus 80 codepoints of context on each side
# (codepoint-safe slicing via jq; byte tools would cut Korean mid-char).
snippet=$(printf '%s' "$prompt" | jq -Rr --arg re "$regex" '
	. as $p
	| [match($re; "ig")] as $ms
	| if ($ms | length) == 0 then empty
	else
		$ms[0] as $m
		| ($m.offset - 80 | if . < 0 then 0 else . end) as $s
		| $p[$s:($m.offset + $m.length + 80)]
	end' 2>/dev/null)
[ -z "$snippet" ] && exit 0

# No CLI on PATH — nothing to accrue through; stay silent.
command -v flint >/dev/null 2>&1 || exit 0

# topic — a short label from the snippet head (40 codepoints).
topic=$(printf '%s' "$snippet" | jq -Rr '.[0:40]' 2>/dev/null)
[ -z "$topic" ] && exit 0

payload=$(jq -nc --arg t "교정: $topic" --arg s "$snippet" \
	'{topic:$t, summary:$s, confidence:0.4}' 2>/dev/null)
[ -z "$payload" ] && exit 0

# Accrual — best-effort, single line: output and errors are discarded,
# a flint failure never fails the hook (exit 0 either way).
printf '%s\n' "$payload" | flint write knowledge --source correction >/dev/null 2>&1 || true
exit 0

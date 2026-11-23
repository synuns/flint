#!/usr/bin/env bash
set -u

[ "${FLINT_CORRECTION_HOOK:-}" = "off" ] && exit 0

regex='그게 *아니라|틀렸어|아니야|다시( ?해| ?줘| ?보자| ?알려)|(^|[^A-Za-z])actually|(^|[^A-Za-z])no,|that.s wrong|correction:'

prompt=$(jq -r '.prompt // empty' 2>/dev/null)
[ -z "$prompt" ] && exit 0

snippet=$(printf '%s' "$prompt" | jq -Rsr --arg re "$regex" '
	. as $p
	| [match($re; "ig")] as $ms
	| if ($ms | length) == 0 then empty
	else
		$ms[0] as $m
		| ($m.offset - 80 | if . < 0 then 0 else . end) as $s
		| $p[$s:($m.offset + $m.length + 80)]
	end' 2>/dev/null)
[ -z "$snippet" ] && exit 0

command -v flint >/dev/null 2>&1 || exit 0

topic=$(printf '%s' "$snippet" | jq -Rsr '.[0:40]' 2>/dev/null)
[ -z "$topic" ] && exit 0

payload=$(jq -nc --arg t "교정: $topic" --arg s "$snippet" \
	'{topic:$t, summary:$s, confidence:0.4}' 2>/dev/null)
[ -z "$payload" ] && exit 0

printf '%s\n' "$payload" | flint write knowledge --source correction >/dev/null 2>&1 || true
exit 0

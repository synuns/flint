#!/usr/bin/env bash
# correction_filter_test.sh — contract tests for the UserPromptSubmit
# correction filter hook (v1.14.0 T2, plan A). Default seam: a fake
# `flint` recorder prepended to PATH records argv + stdin payload and
# never touches a DB. SUPERLITE_REAL=1 swaps the real CLI build in (the
# same cases run against the actual write path; requires FLINT_DSN or
# FLINT_TEST_DSN, otherwise the real pass is skipped).
# Gates (the plan go/no-go bar): recall >= 8/10 on synthetic correction
# prompts and 0 false positives on 10 non-correction prompts.
set -u

HOOK_DIR=$(cd "$(dirname "$0")" && pwd)
HOOK="$HOOK_DIR/correction_filter.sh"
ROOT=$(cd "$HOOK_DIR/../.." && pwd)
TMP=$(mktemp -d) || exit 2
trap 'rm -rf "$TMP"' EXIT

FLINT_LOG="$TMP/calls.log"
export FLINT_LOG
PASS=0
FAIL=0

ok() { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s — %s\n' "$1" "$2"; }

# fake flint — records one invocation as two lines: "CALL args:<argv>"
# then the stdin payload. The real seam replaces this binary below.
mkdir -p "$TMP/bin"
cat > "$TMP/bin/flint" <<'EOF'
#!/usr/bin/env bash
{ printf 'CALL args:%s\n' "$*"; cat; printf '\n'; } >> "$FLINT_LOG"
exit 0
EOF
chmod +x "$TMP/bin/flint"
PATH="$TMP/bin:$PATH"
export PATH

# run_hook PROMPT_JSON — pipes the hook stdin JSON, prints hook exit.
# "$BASH" (absolute) so a PATH-stripped case still finds the interpreter.
run_hook() {
	printf '%s' "$1" | "$BASH" "$HOOK" >/dev/null 2>&1
	echo $?
}

call_count() {
	[ -f "$FLINT_LOG" ] || { echo 0; return; }
	grep -c '^CALL ' "$FLINT_LOG" 2>/dev/null || echo 0
}

payload_of() {
	awk '/^CALL /{getline; print; exit}' "$FLINT_LOG" 2>/dev/null
}

# ── 1. kill switch: FLINT_CORRECTION_HOOK=off exits 0, zero flint calls ──
rm -f "$FLINT_LOG"
st=$(FLINT_CORRECTION_HOOK=off run_hook '{"prompt":"그게 아니라 배포는 금요일이야"}')
if [ "$st" = 0 ] && [ "$(call_count)" = 0 ]; then
	ok "off switch: exit 0, no flint call"
else
	bad "off switch" "exit=$st calls=$(call_count), want 0/0"
fi

# ── 2. non-correction prompt: exit 0, no output, zero DB writes ──
rm -f "$FLINT_LOG"
out=$(printf '%s' '{"prompt":"Add a unit test for the parser edge case"}' | bash "$HOOK" 2>/dev/null)
st=$?
if [ "$st" = 0 ] && [ -z "$out" ] && [ "$(call_count)" = 0 ]; then
	ok "non-match: exit 0, silent, no flint call"
else
	bad "non-match" "exit=$st out=<$out> calls=$(call_count), want 0/empty/0"
fi

# ── 3. Korean marker: one call, correction source, confidence 0.4 ──
rm -f "$FLINT_LOG"
st=$(run_hook '{"prompt":"발표 자료 정리해줘. 그게 아니라 배포는 금요일이야, 지난주에 정했잖아. 그리고 로고도 새걸로 바꿨어."}')
n=$(call_count)
args=$(sed -n 's/^CALL args://p' "$FLINT_LOG" 2>/dev/null | head -1)
payload=$(payload_of)
conf=$(printf '%s' "$payload" | jq -r '.confidence // empty' 2>/dev/null)
has_marker=$(printf '%s' "$payload" | jq -r '.summary | contains("그게 아니라")' 2>/dev/null)
if [ "$st" = 0 ] && [ "$n" = 1 ] \
	&& [ "$args" = "write knowledge --source correction" ] \
	&& [ "$conf" = "0.4" ] && [ "$has_marker" = "true" ]; then
	ok "ko marker: 1 call, --source correction, confidence 0.4, marker in summary"
else
	bad "ko marker" "exit=$st n=$n args=<$args> conf=$conf marker=$has_marker"
fi

# ── 4. English marker "Actually," ──
rm -f "$FLINT_LOG"
st=$(run_hook '{"prompt":"Deploy notes: check the badge first. Actually the port is 5434, not 5432. Update the README table too."}')
n=$(call_count)
conf=$(printf '%s' "$(payload_of)" | jq -r '.confidence // empty' 2>/dev/null)
if [ "$st" = 0 ] && [ "$n" = 1 ] && [ "$conf" = "0.4" ]; then
	ok "en marker: 1 call, confidence 0.4"
else
	bad "en marker" "exit=$st n=$n conf=$conf"
fi

# ── 5. explicit "correction:" marker ──
rm -f "$FLINT_LOG"
st=$(run_hook '{"prompt":"correction: the review starts at 3pm, not 2pm"}')
n=$(call_count)
if [ "$st" = 0 ] && [ "$n" = 1 ]; then
	ok "correction: marker matched"
else
	bad "correction: marker" "exit=$st n=$n, want 0/1"
fi

# ── 6. context window: snippet bounded to marker +/- 80 codepoints ──
rm -f "$FLINT_LOG"
long_ko=$(printf '가%.0s' $(seq 1 200))"그게 아니라 값은 5야"$(printf '나%.0s' $(seq 1 200))
st=$(run_hook "{\"prompt\":\"$long_ko\"}")
len=$(printf '%s' "$(payload_of)" | jq -r '.summary | length' 2>/dev/null)
# marker "그게 아니라 값은 5야" = 10 codepoints; bound = 80+10+80 (+2 slack).
if [ "$st" = 0 ] && [ "$len" -le 172 ] && [ "$len" -ge 90 ] 2>/dev/null; then
	ok "context window: summary length $len within marker+/-80"
else
	bad "context window" "exit=$st summary length=$len, want 90..172"
fi

# ── 7. flint failure is non-fatal (non-interference) ──
rm -f "$FLINT_LOG"
cat > "$TMP/bin/flint" <<'EOF'
#!/usr/bin/env bash
exit 1
EOF
chmod +x "$TMP/bin/flint"
st=$(run_hook '{"prompt":"그게 아니라 다른 값이야"}')
if [ "$st" = 0 ]; then
	ok "flint failure: hook still exits 0"
else
	bad "flint failure" "exit=$st, want 0"
fi
# restore the recording fake for the measurement below.
cat > "$TMP/bin/flint" <<'EOF'
#!/usr/bin/env bash
{ printf 'CALL args:%s\n' "$*"; cat; printf '\n'; } >> "$FLINT_LOG"
exit 0
EOF
chmod +x "$TMP/bin/flint"

# ── 8. invalid stdin JSON: exit 0, zero calls ──
rm -f "$FLINT_LOG"
st=$(printf 'not json at all' | bash "$HOOK" >/dev/null 2>&1; echo $?)
if [ "$st" = 0 ] && [ "$(call_count)" = 0 ]; then
	ok "invalid stdin: exit 0, no call"
else
	bad "invalid stdin" "exit=$st calls=$(call_count), want 0/0"
fi

# ── 9. flint missing from PATH: exit 0 (never blocks the prompt) ──
rm -f "$FLINT_LOG"
st=$(PATH="$TMP/emptybin" run_hook '{"prompt":"그게 아니라 값은 5야"}')
if [ "$st" = 0 ]; then
	ok "no flint on PATH: exit 0"
else
	bad "no flint on PATH" "exit=$st, want 0"
fi

# ── 10. hooks.json registration (pattern: timeout, || true) ──
reg=$(jq -r '.hooks.UserPromptSubmit[0].hooks[0] |
	if .command and (.command | contains("correction_filter.sh"))
		and (.command | contains("|| true"))
		and .timeout == 10 then "ok" else "ng" end' "$HOOK_DIR/hooks.json" 2>/dev/null)
if [ "$reg" = "ok" ]; then
	ok "hooks.json: UserPromptSubmit registered, || true, timeout 10"
else
	bad "hooks.json registration" "got <$reg>"
fi

# ── 11. reproduction-rate gate (plan go/no-go): 10+10 synthetic prompts ──
CORRECTIONS=(
	'그게 아니라 배포는 금요일에 하기로 했어'
	'아니야, 그 값은 3이 아니라 5야'
	'틀렸어. 올바른 커맨드는 make test야'
	'다시 해줘, 이번엔 영문 버전으로'
	'Actually, the port is 5434, not 5432'
	'No, we use Doltgres, not plain Postgres'
	'that'"'"'s wrong — the flag is --source correction'
	'correction: the meeting is at 3pm, not 2pm'
	'그게 아니라 엔드포인트는 v2를 써야 해'
	'Actually the timeout is 10 seconds'
)
PLAIN=(
	'flint read 배포 정책 보여줘'
	'Add a unit test for the parser edge case'
	'What is the default confidence threshold?'
	'Summarize the current migration plan'
	'지난 회의록 요약해줘'
	'Refactor the export function to use fewer allocations'
	'How do I run the e2e suite locally?'
	'스키마 변경사항 정리해서 보여줘'
	'Please review the PR and leave comments'
	'Check the release checklist before tagging'
)
recall=0
for p in "${CORRECTIONS[@]}"; do
	rm -f "$FLINT_LOG"
	run_hook "{\"prompt\":\"$p\"}" >/dev/null
	[ "$(call_count)" -ge 1 ] && recall=$((recall + 1))
done
false_pos=0
for p in "${PLAIN[@]}"; do
	rm -f "$FLINT_LOG"
	run_hook "{\"prompt\":\"$p\"}" >/dev/null
	[ "$(call_count)" -ge 1 ] && false_pos=$((false_pos + 1))
done
printf 'measurement: recall %d/10, false positives %d/10\n' "$recall" "$false_pos"
if [ "$recall" -ge 8 ] && [ "$false_pos" -eq 0 ]; then
	ok "go/no-go gate: recall>=80%, FP=0"
else
	bad "go/no-go gate" "recall=$recall/10 FP=$false_pos/10, want >=8 and 0"
fi

# ── 12. bare "다시" false positive (R57): casual reuse must not match ──
rm -f "$FLINT_LOG"
st=$(run_hook '{"prompt":"내일 다시 얘기하자 — 오늘은 여기까지 하자"}')
n=$(call_count)
if [ "$st" = 0 ] && [ "$n" = 0 ]; then
	ok "bare 다시: casual reuse, no flint call"
else
	bad "bare 다시" "exit=$st calls=$n, want 0/0"
fi

# ── 13. contextualized "다시" still matches (recall kept) ──
rm -f "$FLINT_LOG"
st=$(run_hook '{"prompt":"다시 알려줘, 올바른 포트는 5434야"}')
n=$(call_count)
if [ "$st" = 0 ] && [ "$n" = 1 ]; then
	ok "다시 + follow-up verb: still matches"
else
	bad "다시 + follow-up verb" "exit=$st calls=$n, want 0/1"
fi

# ── 14. two matched lines accrue ONE fragment (first-match contract) ──
rm -f "$FLINT_LOG"
fill_a=$(printf '가%.0s' $(seq 1 100))
fill_b=$(printf '나%.0s' $(seq 1 100))
p14=$(printf '%s\n%s' "${fill_a}그게 아니라 값은 5야${fill_b}" '아니야 둘째 줄에도 마커가 있다')
st=$(run_hook "$(jq -cn --arg p "$p14" '{prompt:$p}')")
n=$(call_count)
payload=$(payload_of)
has_first=$(printf '%s' "$payload" | jq -r '.summary | contains("그게 아니라")' 2>/dev/null)
has_second=$(printf '%s' "$payload" | jq -r '.summary | contains("아니야")' 2>/dev/null)
if [ "$st" = 0 ] && [ "$n" = 1 ] && [ "$has_first" = "true" ] && [ "$has_second" = "false" ]; then
	ok "two matched lines: 1 call, first fragment only"
else
	bad "two matched lines" "exit=$st n=$n first=$has_first second=$has_second, want 0/1/true/false"
fi

# ── 15. newline-spanning context: +/-80 window crosses line breaks ──
rm -f "$FLINT_LOG"
p15=$(printf '%s\n%s' '짧은 서론. 그게 아니라 포트는 5434야' '둘째 줄 내용도 맥락에 포함된다')
st=$(run_hook "$(jq -cn --arg p "$p15" '{prompt:$p}')")
payload=$(payload_of)
spans=$(printf '%s' "$payload" | jq -r '.summary | contains("둘째 줄")' 2>/dev/null)
if [ "$st" = 0 ] && [ "$spans" = "true" ]; then
	ok "newline-spanning context: second line inside snippet"
else
	bad "newline-spanning context" "exit=$st spans=$spans, want 0/true"
fi

# ── real-producer pass (SUPERLITE_REAL=1): the same hook runs against
# the built CLI on the real write path. Skipped without a DSN. ──
if [ "${SUPERLITE_REAL:-0}" = "1" ]; then
	if [ -n "${FLINT_TEST_DSN:-}" ]; then
		export FLINT_DSN="$FLINT_TEST_DSN"
	fi
	if [ -n "${FLINT_DSN:-}" ] && go build -o "$TMP/bin/flint" "$ROOT/cmd/flint" 2>/dev/null; then
		rm -f "$FLINT_LOG"
		st=$(run_hook '{"prompt":"그게 아니라 값은 5야 — real seam demo"}')
		if [ "$st" = 0 ]; then
			ok "real seam: hook exit 0 against the built CLI"
		else
			bad "real seam" "exit=$st, want 0"
		fi
	else
		printf 'skip real seam: SUPERLITE_REAL=1 without FLINT_DSN/FLINT_TEST_DSN or build failed\n'
	fi
fi

printf 'summary: pass=%d fail=%d\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] || exit 1
exit 0

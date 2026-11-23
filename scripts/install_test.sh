#!/usr/bin/env bash
set -u

ROOT=$(cd "$(dirname "$0")/.." && pwd)
INSTALL="$ROOT/scripts/install.sh"
WORK=$(mktemp -d) || exit 2
trap 'rm -rf "$WORK"' EXIT

FAKEBIN="$WORK/fakebin"
FIXTURES="$WORK/fixtures"
BIN="$WORK/bin"
export FLINT_FIXTURES="$FIXTURES"
export FLINT_CURL_LOG="$WORK/curl.log"
export FLINT_SHASUM_LOG="$WORK/shasum.log"
export FLINT_SHA256SUM_LOG="$WORK/sha256sum.log"

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s — %s\n' "$1" "$2"; }

sha256_of() {
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	elif command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	else
		openssl dgst -sha256 -r "$1" | awk '{print $1}'
	fi
}

bogus_hash() { printf 'deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef'; }

write_stubs() {
	mkdir -p "$FAKEBIN"
	cat > "$FAKEBIN/uname" <<'EOF'
#!/usr/bin/env bash
case "${1:-}" in
	-s) echo "${FLINT_FAKE_UNAME_S:-Darwin}" ;;
	-m) echo "${FLINT_FAKE_UNAME_M:-arm64}" ;;
	*) exit 1 ;;
esac
EOF
	cat > "$FAKEBIN/curl" <<'EOF'
#!/usr/bin/env bash
out=""
url=""
args=("$@")
i=0
while [ "$i" -lt "${#args[@]}" ]; do
	a="${args[$i]}"
	case "$a" in
		-o) i=$((i + 1)); out="${args[$i]}" ;;
		-*) ;;
		*) url="$a" ;;
	esac
	i=$((i + 1))
done
[ -n "$url" ] || { echo "fake-curl: no url in: $*" >&2; exit 2; }
printf 'GET %s\n' "$url" >> "$FLINT_CURL_LOG"
src="$FLINT_FIXTURES/$(basename "$url")"
if [ ! -f "$src" ]; then
	echo "fake-curl: 404 $url" >&2
	exit 22
fi
if [ -n "$out" ]; then
	cp "$src" "$out"
else
	cat "$src"
fi
EOF
	chmod +x "$FAKEBIN/uname" "$FAKEBIN/curl"
}

write_release_json() {
	{
		printf '{"tag_name":"v9.9.9",\n"assets":[\n'
		first=1
		for o in darwin linux; do
			for a in arm64 amd64; do
				[ "$first" = 1 ] || printf ',\n'
				first=0
				printf '{"name":"flint_9.9.9_%s_%s.tar.gz","browser_download_url":"https://github.com/synuns/flint/releases/download/v9.9.9/flint_9.9.9_%s_%s.tar.gz"}' \
					"$o" "$a" "$o" "$a"
			done
		done
		printf ',\n{"name":"flint_plugin_9.9.9.zip","browser_download_url":"https://github.com/synuns/flint/releases/download/v9.9.9/flint_plugin_9.9.9.zip"}'
		printf '\n,{"name":"flint_9.9.9_darwin_arm64.mcpb","browser_download_url":"https://github.com/synuns/flint/releases/download/v9.9.9/flint_9.9.9_darwin_arm64.mcpb"}'
		printf '\n,{"name":"checksums.txt","browser_download_url":"https://github.com/synuns/flint/releases/download/v9.9.9/checksums.txt"}'
		printf '\n]}\n'
	} > "$FIXTURES/latest"
}

make_release() {
	tos="$1"
	tarch="$2"
	rm -rf "$FIXTURES" "$WORK/stage"
	mkdir -p "$FIXTURES" "$WORK/stage"
	target="flint_9.9.9_${tos}_${tarch}.tar.gz"
	stage="$WORK/stage"
	printf '#!/usr/bin/env bash\necho flint-fake-9.9.9\n' > "$stage/flint"
	chmod +x "$stage/flint"
	printf 'demo license\n' > "$stage/LICENSE"
	( cd "$stage" && tar -czf "$FIXTURES/$target" flint LICENSE )
	for o in darwin linux; do
		for a in arm64 amd64; do
			n="flint_9.9.9_${o}_${a}.tar.gz"
			[ "$n" = "$target" ] || cp "$FIXTURES/$target" "$FIXTURES/$n"
		done
	done
	hash="$(sha256_of "$FIXTURES/$target")"
	{
		for o in darwin linux; do
			for a in arm64 amd64; do
				n="flint_9.9.9_${o}_${a}.tar.gz"
				if [ "$n" = "$target" ]; then
					printf '%s  %s\n' "$hash" "$n"
				else
					printf '%s  %s\n' "$(bogus_hash)" "$n"
				fi
			done
		done
		printf '%s  flint_plugin_9.9.9.zip\n' "$(bogus_hash)"
		printf '%s  flint_9.9.9_darwin_arm64.mcpb\n' "$(bogus_hash)"
	} > "$FIXTURES/checksums.txt"
	write_release_json
}

reset_case() {
	rm -rf "$FAKEBIN" "$BIN"
	mkdir -p "$BIN"
	write_stubs
	: > "$FLINT_CURL_LOG"
	rm -f "$FLINT_SHASUM_LOG" "$FLINT_SHA256SUM_LOG"
	export FLINT_FAKE_UNAME_S=Darwin
	export FLINT_FAKE_UNAME_M=arm64
}

run_install() {
	OUT="$(PATH="$FAKEBIN:$PATH" "$BASH" "$INSTALL" "$@" 2>&1)"
	ST=$?
}

add_shasum_stub() {
	mode="$1"
	cat > "$FAKEBIN/shasum" <<EOF
#!/usr/bin/env bash
printf 'shasum %s\n' "\$*" >> "\$FLINT_SHASUM_LOG"
[ "$mode" = absent ] && exit 127
exit 0
EOF
	chmod +x "$FAKEBIN/shasum"
}

add_sha256sum_stub() {
	mode="$1"
	cat > "$FAKEBIN/sha256sum" <<EOF
#!/usr/bin/env bash
printf 'sha256sum %s\n' "\$*" >> "\$FLINT_SHA256SUM_LOG"
[ "$mode" = absent ] && exit 127
exit 0
EOF
	chmod +x "$FAKEBIN/sha256sum"
}

reset_case
run_install --help
if [ "$ST" = 0 ] && printf '%s' "$OUT" | grep -q -- '--bin-dir' && [ ! -s "$FLINT_CURL_LOG" ]; then
	ok "--help prints usage, makes no requests"
else
	bad "--help" "exit=$ST"
fi

reset_case
make_release darwin arm64
run_install --bin-dir "$BIN"
if [ "$ST" = 0 ] \
	&& grep -q 'flint_9.9.9_darwin_arm64\.tar\.gz$' "$FLINT_CURL_LOG" \
	&& ! grep -q 'linux' "$FLINT_CURL_LOG" \
	&& [ -x "$BIN/flint" ] \
	&& [ "$("$BIN/flint")" = "flint-fake-9.9.9" ] \
	&& printf '%s' "$OUT" | grep -q 'Installed flint v9.9.9' \
	&& printf '%s' "$OUT" | grep -q 'Doltgres' \
	&& printf '%s' "$OUT" | grep -q 'prebuilt'; then
	ok "darwin/arm64: correct asset, checksum line filter, install"
else
	bad "darwin/arm64" "exit=$ST out=$OUT"
fi

reset_case
make_release linux amd64
export FLINT_FAKE_UNAME_S=Linux
export FLINT_FAKE_UNAME_M=x86_64
run_install --bin-dir "$BIN"
if [ "$ST" = 0 ] \
	&& grep -q 'flint_9.9.9_linux_amd64\.tar\.gz$' "$FLINT_CURL_LOG" \
	&& ! grep -q 'darwin_arm64\.tar\.gz$' "$FLINT_CURL_LOG" \
	&& [ -x "$BIN/flint" ]; then
	ok "linux/amd64: correct asset selected"
else
	bad "linux/amd64" "exit=$ST out=$OUT"
fi

reset_case
make_release darwin arm64
run_install --bin-dir "$BIN"
out_nopath="$OUT"
out_inpath="$(PATH="$BIN:$FAKEBIN:$PATH" "$BASH" "$INSTALL" --bin-dir "$BIN" 2>&1)"
if [ "$ST" = 0 ] \
	&& printf '%s' "$out_nopath" | grep -q 'not on your PATH' \
	&& ! printf '%s' "$out_inpath" | grep -q 'not on your PATH'; then
	ok "PATH guidance only when bin dir is outside PATH"
else
	bad "PATH guidance" "nopath_exit=$ST"
fi

reset_case
make_release darwin arm64
export FLINT_FAKE_UNAME_S=Windows_NT
run_install --bin-dir "$BIN"
if [ "$ST" != 0 ] && printf '%s' "$OUT" | grep -qi 'unsupported OS'; then
	ok "unsupported OS rejected"
else
	bad "unsupported OS" "exit=$ST"
fi

reset_case
make_release darwin arm64
export FLINT_FAKE_UNAME_M=i686
run_install --bin-dir "$BIN"
if [ "$ST" != 0 ] && printf '%s' "$OUT" | grep -qi 'unsupported architecture'; then
	ok "unsupported architecture rejected"
else
	bad "unsupported arch" "exit=$ST"
fi

reset_case
make_release darwin arm64
grep -v '"name":"flint_9.9.9_darwin_arm64.tar.gz"' "$FIXTURES/latest" > "$FIXTURES/latest.new"
mv "$FIXTURES/latest.new" "$FIXTURES/latest"
run_install --bin-dir "$BIN"
if [ "$ST" != 0 ] && printf '%s' "$OUT" | grep -qi 'no release asset' && [ ! -e "$BIN/flint" ]; then
	ok "missing platform asset rejected"
else
	bad "missing asset" "exit=$ST"
fi

reset_case
make_release darwin arm64
grep -v '"name":"checksums.txt"' "$FIXTURES/latest" > "$FIXTURES/latest.new"
mv "$FIXTURES/latest.new" "$FIXTURES/latest"
run_install --bin-dir "$BIN"
if [ "$ST" != 0 ] && printf '%s' "$OUT" | grep -qi 'checksums.txt' && [ ! -e "$BIN/flint" ]; then
	ok "missing checksums.txt asset refused"
else
	bad "missing checksums asset" "exit=$ST"
fi

reset_case
make_release darwin arm64
awk -v t="flint_9.9.9_darwin_arm64.tar.gz" -v h="$(bogus_hash)" \
	'$2 == t { print h "  " t; next } { print }' \
	"$FIXTURES/checksums.txt" > "$FIXTURES/checksums.new"
mv "$FIXTURES/checksums.new" "$FIXTURES/checksums.txt"
run_install --bin-dir "$BIN"
if [ "$ST" != 0 ] && printf '%s' "$OUT" | grep -qi 'verification failed' && [ ! -e "$BIN/flint" ]; then
	ok "checksum mismatch rejected, nothing installed"
else
	bad "checksum mismatch" "exit=$ST out=$OUT"
fi

reset_case
make_release darwin arm64
grep -v '  flint_9.9.9_darwin_arm64.tar.gz$' "$FIXTURES/checksums.txt" > "$FIXTURES/checksums.new"
mv "$FIXTURES/checksums.new" "$FIXTURES/checksums.txt"
run_install --bin-dir "$BIN"
if [ "$ST" != 0 ] && printf '%s' "$OUT" | grep -qi 'no entry' && [ ! -e "$BIN/flint" ]; then
	ok "missing checksum line refused"
else
	bad "missing checksum line" "exit=$ST"
fi

reset_case
make_release darwin arm64
rm "$FIXTURES/flint_9.9.9_darwin_arm64.tar.gz"
run_install --bin-dir "$BIN"
if [ "$ST" != 0 ] && [ ! -e "$BIN/flint" ]; then
	ok "download failure rejected"
else
	bad "download failure" "exit=$ST"
fi

reset_case
make_release darwin arm64
add_shasum_stub ok
add_sha256sum_stub ok
run_install --bin-dir "$BIN"
if [ "$ST" = 0 ] \
	&& grep -q -- '-a 256 -c' "$FLINT_SHASUM_LOG" \
	&& [ ! -e "$FLINT_SHA256SUM_LOG" ]; then
	ok "shasum preferred over sha256sum"
else
	bad "shasum preference" "exit=$ST"
fi

reset_case
make_release darwin arm64
add_shasum_stub absent
add_sha256sum_stub ok
run_install --bin-dir "$BIN"
if [ "$ST" = 0 ] \
	&& grep -q -- ' -c ' "$FLINT_SHA256SUM_LOG" \
	&& [ -x "$BIN/flint" ]; then
	ok "sha256sum fallback works"
else
	bad "sha256sum fallback" "exit=$ST"
fi

reset_case
make_release darwin arm64
add_shasum_stub absent
add_sha256sum_stub absent
run_install --bin-dir "$BIN"
if [ "$ST" != 0 ] && [ ! -e "$BIN/flint" ]; then
	ok "missing sha256 tooling rejected"
else
	bad "no sha256 tool" "exit=$ST"
fi

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]

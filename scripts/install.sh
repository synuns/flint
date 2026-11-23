#!/usr/bin/env bash
set -eu

REPO="synuns/flint"
DEFAULT_BIN_DIR="${HOME}/.local/bin"

err() {
	printf 'install.sh: error: %s\n' "$1" >&2
	exit 1
}

ensure() {
	if ! "$@"; then
		err "command failed: $*"
	fi
}

usage() {
	cat <<'EOF'
Usage: bash install.sh [--bin-dir DIR]

Installs the latest prebuilt flint release binary for the current
platform (darwin/linux on arm64/amd64) from GitHub releases.

Options:
  --bin-dir DIR   installation directory (default: ~/.local/bin)
  -h, --help      show this help

The download is verified against the release checksums.txt (sha256).
Runtime prerequisite: Docker — flint setup ships the Doltgres engine
compose definition inside the binary (no repository checkout needed).
No build tools are required — this installs a prebuilt binary.
EOF
}

bin_dir="${DEFAULT_BIN_DIR}"
while [ "$#" -gt 0 ]; do
	case "$1" in
		--bin-dir)
			[ "$#" -ge 2 ] || err "--bin-dir requires a value"
			bin_dir="$2"
			shift 2
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			err "unknown argument: $1 (see --help)"
			;;
	esac
done

os="$(uname -s)"
case "$os" in
	Darwin) os="darwin" ;;
	Linux) os="linux" ;;
	*) err "unsupported OS: $os (supported: darwin, linux)" ;;
esac
arch="$(uname -m)"
case "$arch" in
	arm64|aarch64) arch="arm64" ;;
	x86_64|amd64) arch="amd64" ;;
	*) err "unsupported architecture: $arch (supported: arm64, amd64)" ;;
esac

printf '==> Looking up the latest flint release for %s/%s ...\n' "$os" "$arch"
json="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest")" \
	|| err "failed to fetch release metadata from the GitHub API"
tag="$(printf '%s\n' "$json" | sed -n 's/.*"tag_name"[^"]*"\([^"]*\)".*/\1/p' | head -n 1)"
[ -n "$tag" ] || err "could not parse tag_name from the release metadata"

asset_url="$(printf '%s\n' "$json" | grep -o 'https://[^"]*' \
	| grep "_${os}_${arch}\.tar\.gz$" | head -n 1)"
[ -n "$asset_url" ] || err "no release asset found for ${os}/${arch}"
asset="$(basename "$asset_url")"

checksums_url="$(printf '%s\n' "$json" | grep -o 'https://[^"]*' \
	| grep '/checksums\.txt$' | head -n 1)"
[ -n "$checksums_url" ] \
	|| err "release ${tag} has no checksums.txt asset; refusing to install without checksum verification"

tmp="$(mktemp -d)" || err "mktemp failed"
trap 'rm -rf "$tmp"' EXIT

printf '==> Downloading %s ...\n' "$asset"
ensure curl -fsSL -o "$tmp/$asset" "$asset_url"
ensure curl -fsSL -o "$tmp/checksums.txt" "$checksums_url"

grep " ${asset}\$" "$tmp/checksums.txt" > "$tmp/check.line" \
	|| err "checksums.txt has no entry for ${asset}"
[ "$(wc -l < "$tmp/check.line" | tr -d ' ')" -eq 1 ] \
	|| err "checksums.txt has multiple entries for ${asset}"

verify_checksum() {
	set +e
	shasum -a 256 -c "$1"
	status=$?
	set -e
	if [ "$status" -eq 0 ]; then
		return 0
	fi
	if [ "$status" -eq 127 ]; then
		sha256sum -c "$1"
		return
	fi
	return "$status"
}
( cd "$tmp" && verify_checksum "check.line" ) \
	|| err "sha256 verification failed for ${asset} — the download may be corrupted or tampered with; refusing to install"
printf '==> sha256 verified against checksums.txt\n'

mkdir -p "$tmp/extract" "$bin_dir"
ensure tar -xzf "$tmp/$asset" -C "$tmp/extract"
bin="$(find "$tmp/extract" -type f -name flint | head -n 1)"
[ -n "$bin" ] || err "no flint binary found inside ${asset}"
ensure cp "$bin" "$bin_dir/flint"
ensure chmod +x "$bin_dir/flint"

printf '==> Installed flint %s to %s\n' "$tag" "$bin_dir/flint"
printf '==> No build tools required — a prebuilt release binary was installed.\n'
printf '==> Runtime prerequisite: Docker — run "flint setup" to start the embedded Doltgres engine (no checkout needed).\n'

case ":$PATH:" in
	*":$bin_dir:"*) ;;
	*)
		printf '==> Note: %s is not on your PATH. Add it in your shell profile:\n' "$bin_dir"
		printf '    export PATH="%s:$PATH"\n' "$bin_dir"
		;;
esac

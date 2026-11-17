#!/bin/sh
# wgtui installer
#
#   curl -fsSL https://raw.githubusercontent.com/KsmBl/wgtui/main/install.sh | sh
#
# Options:
#   --source            build from source with Go instead of downloading a release
#   --prefix DIR        install into DIR/bin (default: /usr/local, env PREFIX)
#   --version vX.Y.Z    install a specific release (default: latest, env VERSION)
#   --uninstall         remove wgtui (your configs are kept)
set -eu

REPO="KsmBl/wgtui"
PREFIX="${PREFIX:-/usr/local}"
VERSION="${VERSION:-latest}"
MODE="release"

while [ $# -gt 0 ]; do
	case "$1" in
	--source) MODE="source" ;;
	--prefix) PREFIX="$2"; shift ;;
	--version) VERSION="$2"; shift ;;
	--uninstall) MODE="uninstall" ;;
	-h|--help) sed -n '2,11p' "$0"; exit 0 ;;
	*) echo "unknown option: $1" >&2; exit 1 ;;
	esac
	shift
done

BIN_DIR="$PREFIX/bin"
info() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

SUDO=""
need_sudo() {
	if [ "$(id -u)" -ne 0 ] && ! { mkdir -p "$BIN_DIR" 2>/dev/null && [ -w "$BIN_DIR" ]; }; then
		command -v sudo >/dev/null 2>&1 || die "$BIN_DIR is not writable and sudo is missing"
		SUDO="sudo"
	fi
}

if [ "$MODE" = "uninstall" ]; then
	need_sudo
	$SUDO rm -f "$BIN_DIR/wgtui"
	info "removed $BIN_DIR/wgtui (configs in /etc/wireguard are kept)"
	exit 0
fi

[ "$(uname -s)" = "Linux" ] || die "wgtui only supports Linux"

WORK="$(mktemp -d)" || die "mktemp failed"
[ -n "$WORK" ] && [ -d "$WORK" ] || die "mktemp failed"
cleanup() { rm -rf -- "$WORK"; }
trap cleanup EXIT

if [ "$MODE" = "source" ]; then
	command -v go >/dev/null 2>&1 || die "Go is required for --source (https://go.dev/dl/)"
	if [ -f go.mod ] && grep -q "module github.com/$REPO" go.mod; then
		SRC="$(pwd)"
	else
		command -v git >/dev/null 2>&1 || die "git is required for --source"
		info "cloning $REPO"
		git clone --quiet --depth 1 "https://github.com/$REPO.git" "$WORK/src"
		SRC="$WORK/src"
	fi
	VER="$(cd "$SRC" && git describe --tags --always 2>/dev/null || echo dev)"
	info "building wgtui $VER"
	(cd "$SRC" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VER" -o "$WORK/wgtui" .)
else
	case "$(uname -m)" in
	x86_64|amd64) ARCH="amd64" ;;
	aarch64|arm64) ARCH="arm64" ;;
	armv7*|armv6*) ARCH="arm" ;;
	i386|i686) ARCH="386" ;;
	*) die "unsupported architecture $(uname -m); try --source" ;;
	esac
	if [ "$VERSION" = "latest" ]; then
		URL="https://github.com/$REPO/releases/latest/download/wgtui-linux-$ARCH"
	else
		URL="https://github.com/$REPO/releases/download/$VERSION/wgtui-linux-$ARCH"
	fi
	info "downloading $URL"
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$URL" -o "$WORK/wgtui" || die "download failed; try --source"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO "$WORK/wgtui" "$URL" || die "download failed; try --source"
	else
		die "curl or wget is required"
	fi
fi

chmod 755 "$WORK/wgtui"
need_sudo
$SUDO mkdir -p "$BIN_DIR"
$SUDO install -m 755 "$WORK/wgtui" "$BIN_DIR/wgtui"
info "installed $("$BIN_DIR/wgtui" --version) to $BIN_DIR/wgtui"

# Runtime dependencies
missing=""
for c in wg wg-quick iptables; do
	command -v "$c" >/dev/null 2>&1 || missing="$missing $c"
done
if [ -n "$missing" ]; then
	warn "missing runtime tools:$missing"
	if command -v apt-get >/dev/null 2>&1; then echo "    sudo apt-get install wireguard-tools iptables"
	elif command -v dnf >/dev/null 2>&1; then echo "    sudo dnf install wireguard-tools iptables"
	elif command -v pacman >/dev/null 2>&1; then echo "    sudo pacman -S wireguard-tools iptables"
	elif command -v zypper >/dev/null 2>&1; then echo "    sudo zypper install wireguard-tools iptables"
	elif command -v apk >/dev/null 2>&1; then echo "    sudo apk add wireguard-tools iptables"
	fi
fi
info "run: sudo wgtui   (or try it safely: wgtui --demo)"

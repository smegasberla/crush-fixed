#!/usr/bin/env bash
# Install or remove this build of Crush on macOS and Linux.
#
# Builds the binary from this checkout (never downloads one), installs it under
# a name of your choosing, and verifies the result. Removal is the same script
# with `uninstall`.
#
# Sessions, config and project state live in files this script never writes, so
# the installed binary reads the same sessions as any other Crush build. That is
# why --purge is a separate, explicit flag.
#
# Usage:
#   ./scripts/install.sh                 install as "crush-fixed"
#   ./scripts/install.sh --as crush      install as "crush"
#   ./scripts/install.sh --prefix DIR    install into DIR instead of ~/.local/bin
#   ./scripts/install.sh --dry-run       print what would happen, change nothing
#   ./scripts/install.sh uninstall       remove the installed binary
#   ./scripts/install.sh uninstall --purge   also delete local state (asks first)

set -euo pipefail

BIN_NAME="crush-fixed"
PREFIX="${HOME}/.local/bin"
ACTION="install"
DRY_RUN=0
PURGE=0

usage() {
	sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
}

die() {
	printf 'error: %s\n' "$1" >&2
	exit 1
}

note() {
	printf '%s\n' "$1"
}

# run executes a command, or prints it when --dry-run is set.
run() {
	if [ "${DRY_RUN}" -eq 1 ]; then
		printf 'would run: %s\n' "$*"
		return 0
	fi
	"$@"
}

while [ $# -gt 0 ]; do
	case "$1" in
	install | uninstall)
		ACTION="$1"
		shift
		;;
	--as)
		[ $# -ge 2 ] || die "--as needs a binary name"
		BIN_NAME="$2"
		shift 2
		;;
	--as=*)
		BIN_NAME="${1#*=}"
		shift
		;;
	--prefix)
		[ $# -ge 2 ] || die "--prefix needs a directory"
		PREFIX="$2"
		shift 2
		;;
	--prefix=*)
		PREFIX="${1#*=}"
		shift
		;;
	--dry-run)
		DRY_RUN=1
		shift
		;;
	--purge)
		PURGE=1
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		die "unknown argument: $1 (try --help)"
		;;
	esac
done

case "${BIN_NAME}" in
"" | */* | *\\*)
	die "--as must be a plain file name, not a path"
	;;
esac

case "$(uname -s)" in
Darwin | Linux) ;;
*) die "this script supports macOS and Linux; on Windows run scripts/install.ps1" ;;
esac

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
TARGET="${PREFIX}/${BIN_NAME}"

# state_dirs are the directories a Crush build keeps user data in. They are
# shared with any other Crush install, which is exactly why purging them is
# opt-in.
state_dirs() {
	printf '%s\n' \
		"${HOME}/.config/crush" \
		"${HOME}/.local/share/crush" \
		"${HOME}/.cache/crush"
}

remove_binary() {
	if [ ! -e "${TARGET}" ]; then
		note "nothing to remove at ${TARGET}"
		return 0
	fi
	run rm -f "${TARGET}"
	note "removed ${TARGET}"
}

purge_state() {
	note "These directories hold sessions, config and history, and are shared"
	note "with any other Crush build on this machine:"
	for dir in $(state_dirs); do
		[ -d "${dir}" ] && note "  ${dir}"
	done
	if [ "${DRY_RUN}" -eq 0 ]; then
		printf 'Delete them? This cannot be undone. [y/N] '
		read -r answer
		case "${answer}" in
		y | Y | yes | YES) ;;
		*)
			note "kept state; nothing was deleted"
			return 0
			;;
		esac
	fi
	for dir in $(state_dirs); do
		[ -d "${dir}" ] || continue
		run rm -rf "${dir}"
		note "deleted ${dir}"
	done
}

if [ "${ACTION}" = "uninstall" ]; then
	remove_binary
	if [ "${PURGE}" -eq 1 ]; then
		purge_state
	fi
	note "uninstalled ${BIN_NAME}"
	exit 0
fi

command -v go >/dev/null 2>&1 || die "Go is required to build this checkout (https://go.dev/dl/)"
[ -f "${REPO_DIR}/go.mod" ] || die "no go.mod in ${REPO_DIR}; run this from the repository"

# The version string is baked in so `--version` identifies this build rather
# than reporting "devel" and looking like a random local compile.
VERSION="$(git -C "${REPO_DIR}" describe --tags --always --dirty 2>/dev/null || echo fixed)"
COMMIT="$(git -C "${REPO_DIR}" rev-parse --short HEAD 2>/dev/null || echo unknown)"

note "building ${BIN_NAME} ${VERSION} (${COMMIT}) from ${REPO_DIR}"
build_dir="$(mktemp -d)"
trap 'rm -rf "${build_dir}"' EXIT

if [ "${DRY_RUN}" -eq 1 ]; then
	printf 'would run: (cd %s && go build -trimpath -ldflags %s -o %s .)\n' \
		"${REPO_DIR}" \
		"-X github.com/charmbracelet/crush/internal/version.Version=${VERSION} -X github.com/charmbracelet/crush/internal/version.Commit=${COMMIT}" \
		"${TARGET}"
	note "dry run: nothing was built or installed"
	exit 0
fi

(
	cd "${REPO_DIR}"
	CGO_ENABLED=0 go build -trimpath \
		-ldflags "-X github.com/charmbracelet/crush/internal/version.Version=${VERSION} -X github.com/charmbracelet/crush/internal/version.Commit=${COMMIT}" \
		-o "${build_dir}/${BIN_NAME}" .
) || die "build failed"

# Install through a temporary name in the destination directory, so replacing a
# running binary cannot fail with "text file busy" and a crash cannot leave a
# half-written executable on PATH.
mkdir -p "${PREFIX}"
stage="${TARGET}.install.$$"
cp "${build_dir}/${BIN_NAME}" "${stage}"
chmod 0755 "${stage}"
mv -f "${stage}" "${TARGET}"

# Verify the installed file actually runs: a copy that cannot execute is worse
# than no install, because it looks like one.
if ! "${TARGET}" --version >/dev/null 2>&1; then
	rm -f "${TARGET}"
	die "the installed binary does not run; removed it"
fi

note "installed ${TARGET}"
note "version: $("${TARGET}" --version 2>/dev/null | head -1)"
note ""
note "Sessions and config are shared with any other Crush build:"
note "  config  ${HOME}/.config/crush/crush.json"
note "  data    <project>/.crush"
note ""
case ":${PATH}:" in
*":${PREFIX}:"*) note "${PREFIX} is on PATH" ;;
*) note "add ${PREFIX} to PATH to run ${BIN_NAME} from anywhere" ;;
esac

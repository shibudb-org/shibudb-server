#!/usr/bin/env bash
set -euo pipefail

APP_NAME="shibudb"
REPO_URL="${SHIBUDB_REPO_URL:-https://github.com/shibudb-org/shibudb-server}"
VERSION="${SHIBUDB_VERSION:-latest}"
PREFIX="${SHIBUDB_PREFIX:-/usr/local}"
SKIP_DEPS="${SHIBUDB_SKIP_DEPS:-0}"
KEEP_BUILD_DIR="${SHIBUDB_KEEP_BUILD_DIR:-0}"
GO_VERSION="${SHIBUDB_GO_VERSION:-1.23.7}"

usage() {
	cat <<EOF
Install ShibuDB from source on Linux using bundled FAISS libraries.

Usage:
  scripts/install-linux.sh [options]

Options:
  --version <version>    ShibuDB version tag to install, e.g. 1.0.7 or v1.0.7.
                         Defaults to latest GitHub release.
  --prefix <path>        Install prefix. Defaults to /usr/local.
  --source <path>        Build from an existing source checkout.
  --skip-deps            Do not install OS build dependencies.
  --keep-build-dir       Keep the temporary build directory for debugging.
  -h, --help             Show this help.

Environment:
  SHIBUDB_VERSION        Same as --version.
  SHIBUDB_PREFIX         Same as --prefix.
  SHIBUDB_REPO_URL       Repository URL. Defaults to $REPO_URL.
  SHIBUDB_SKIP_DEPS=1    Same as --skip-deps.
  SHIBUDB_KEEP_BUILD_DIR=1
  SHIBUDB_GO_VERSION     Go version used if a suitable go command is not found.
EOF
}

SOURCE_DIR=""

while [[ $# -gt 0 ]]; do
	case "$1" in
		--version)
			VERSION="${2:-}"
			shift 2
			;;
		--prefix)
			PREFIX="${2:-}"
			shift 2
			;;
		--source)
			SOURCE_DIR="${2:-}"
			shift 2
			;;
		--skip-deps)
			SKIP_DEPS=1
			shift
			;;
		--keep-build-dir)
			KEEP_BUILD_DIR=1
			shift
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			echo "Unknown option: $1" >&2
			usage >&2
			exit 1
			;;
	esac
done

if [[ "$(uname -s)" != "Linux" ]]; then
	echo "This installer supports Linux only." >&2
	exit 1
fi

if ldd --version 2>&1 | grep -qi musl; then
	echo "This installer does not support musl/Alpine Linux." >&2
	exit 1
fi

case "$(uname -m)" in
	x86_64)
		LIB_ARCH="amd64"
		GO_ARCH="amd64"
		;;
	aarch64|arm64)
		LIB_ARCH="arm64"
		GO_ARCH="arm64"
		;;
	*)
		echo "Unsupported architecture: $(uname -m)" >&2
		exit 1
		;;
esac

if [[ -z "$PREFIX" || "$PREFIX" != /* ]]; then
	echo "--prefix must be an absolute path." >&2
	exit 1
fi

BIN_DIR="$PREFIX/bin"
LIB_DIR="$PREFIX/lib"
SHARE_DIR="$PREFIX/share/$APP_NAME"

if [[ "${EUID:-$(id -u)}" -eq 0 ]]; then
	SUDO=""
else
	if ! command -v sudo >/dev/null 2>&1; then
		echo "This installer needs sudo when not run as root." >&2
		exit 1
	fi
	SUDO="sudo"
fi

need_cmd() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "Missing required command: $1" >&2
		exit 1
	fi
}

install_deps() {
	if [[ "$SKIP_DEPS" == "1" ]]; then
		echo "Skipping dependency installation."
		return
	fi

	if command -v apt-get >/dev/null 2>&1; then
		$SUDO apt-get update
		$SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y \
			build-essential \
			ca-certificates \
			curl \
			g++ \
			gcc \
			git \
			libc6-dev \
			libgomp1 \
			libopenblas-dev \
			libstdc++6 \
			make \
			tar
	elif command -v dnf >/dev/null 2>&1; then
		$SUDO dnf install -y dnf-plugins-core || true
		$SUDO dnf config-manager --set-enabled powertools || true
		$SUDO dnf config-manager --set-enabled PowerTools || true
		$SUDO dnf config-manager --set-enabled crb || true
		$SUDO dnf install -y epel-release || true
		if ! $SUDO dnf install -y \
			ca-certificates \
			gcc \
			gcc-c++ \
			libgomp \
			make \
			openblas \
			tar; then
			$SUDO dnf install -y --allowerasing \
				ca-certificates \
				gcc \
				gcc-c++ \
				libgomp \
				make \
				openblas \
				tar
		fi
		$SUDO dnf install -y git || \
			$SUDO dnf install -y --allowerasing git || true
		$SUDO dnf install -y openblas-devel || \
			$SUDO dnf install -y --nobest openblas-devel || true
	elif command -v yum >/dev/null 2>&1; then
		if command -v amazon-linux-extras >/dev/null 2>&1; then
			$SUDO amazon-linux-extras install epel -y || true
		fi
		$SUDO yum install -y epel-release || true
		$SUDO yum install -y \
			ca-certificates \
			curl \
			gcc \
			gcc-c++ \
			libgomp \
			make \
			openblas \
			tar
		$SUDO yum install -y git || true
		$SUDO yum install -y openblas-devel || true
	else
		echo "Unsupported Linux package manager. Install build tools, Go, OpenBLAS, libgomp, and libstdc++ manually, then rerun with --skip-deps." >&2
		exit 1
	fi
}

version_at_least() {
	# Returns success if $1 >= $2.
	[[ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -n1)" == "$2" ]]
}

prepare_go() {
	if command -v go >/dev/null 2>&1; then
		local current
		current="$(go env GOVERSION 2>/dev/null | sed 's/^go//')"
		if [[ -n "$current" ]] && version_at_least "$current" "1.23.0"; then
			echo "Using existing Go: $(go version)"
			return
		fi
	fi

	echo "Installing temporary Go $GO_VERSION for this build..."
	local go_tar="go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
	curl -fsSL "https://go.dev/dl/${go_tar}" -o "$WORK_DIR/$go_tar"
	mkdir -p "$WORK_DIR/go"
	tar -C "$WORK_DIR/go" --strip-components=1 -xzf "$WORK_DIR/$go_tar"
	export PATH="$WORK_DIR/go/bin:$PATH"
	go version
}

resolve_latest_tag() {
	local latest_url tag
	latest_url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$REPO_URL/releases/latest")"
	tag="${latest_url##*/}"
	if [[ -z "$tag" || "$tag" == "latest" ]]; then
		echo "Could not resolve latest ShibuDB release." >&2
		exit 1
	fi
	echo "$tag"
}

prepare_source() {
	if [[ -n "$SOURCE_DIR" ]]; then
		SOURCE_DIR="$(cd "$SOURCE_DIR" && pwd)"
	elif [[ -f "go.mod" && -f "main.go" && -d "resources/lib/linux" ]]; then
		SOURCE_DIR="$(pwd)"
	else
		local tag archive_url archive
		if [[ "$VERSION" == "latest" ]]; then
			tag="$(resolve_latest_tag)"
		elif [[ "$VERSION" == main ]]; then
			tag="main"
		elif [[ "$VERSION" == v* ]]; then
			tag="$VERSION"
		else
			tag="v$VERSION"
		fi

		echo "Downloading ShibuDB source: $tag"
		if [[ "$tag" == "main" ]]; then
			archive_url="$REPO_URL/archive/refs/heads/main.tar.gz"
		else
			archive_url="$REPO_URL/archive/refs/tags/$tag.tar.gz"
		fi
		archive="$WORK_DIR/source.tar.gz"
		curl -fsSL "$archive_url" -o "$archive"
		mkdir -p "$WORK_DIR/source"
		tar -C "$WORK_DIR/source" --strip-components=1 -xzf "$archive"
		SOURCE_DIR="$WORK_DIR/source"
	fi

	if [[ ! -f "$SOURCE_DIR/main.go" || ! -f "$SOURCE_DIR/go.mod" ]]; then
		echo "Invalid source directory: $SOURCE_DIR" >&2
		exit 1
	fi

	if [[ ! -f "$SOURCE_DIR/resources/lib/linux/$LIB_ARCH/libfaiss.so" ||
		! -f "$SOURCE_DIR/resources/lib/linux/$LIB_ARCH/libfaiss_c.so" ]]; then
		echo "Missing bundled FAISS libraries for $LIB_ARCH in $SOURCE_DIR/resources/lib/linux/$LIB_ARCH" >&2
		exit 1
	fi
}

install_files() {
	local build_bin="$WORK_DIR/$APP_NAME"
	local faiss_lib_dir="$SOURCE_DIR/resources/lib/linux/$LIB_ARCH"

	$SUDO install -d "$BIN_DIR" "$LIB_DIR" "$SHARE_DIR"
	$SUDO install -m 0755 "$build_bin" "$BIN_DIR/$APP_NAME"
	$SUDO install -m 0755 "$faiss_lib_dir/libfaiss.so" "$LIB_DIR/libfaiss.so"
	$SUDO install -m 0755 "$faiss_lib_dir/libfaiss_c.so" "$LIB_DIR/libfaiss_c.so"
	$SUDO cp -R "$SOURCE_DIR/resources" "$SHARE_DIR/" 2>/dev/null || true
	$SUDO install -m 0644 "$SOURCE_DIR/LICENSE" "$SHARE_DIR/LICENSE" 2>/dev/null || true
	$SUDO install -m 0644 "$SOURCE_DIR/README.md" "$SHARE_DIR/README.md" 2>/dev/null || true

	register_libraries
}

find_ldconfig() {
	# ldconfig commonly lives in /sbin or /usr/sbin, which are not on a normal
	# user's PATH (notably on Debian/Ubuntu). Resolve it explicitly so the
	# registration step is not silently skipped.
	local candidate
	if candidate="$(command -v ldconfig 2>/dev/null)"; then
		echo "$candidate"
		return 0
	fi
	for candidate in /sbin/ldconfig /usr/sbin/ldconfig /usr/bin/ldconfig /bin/ldconfig; do
		if [[ -x "$candidate" ]]; then
			echo "$candidate"
			return 0
		fi
	done
	return 1
}

register_libraries() {
	# Make $LIB_DIR resolvable by the dynamic linker at runtime. We always
	# write the ld.so.conf.d entry (creating the directory if needed) and run
	# ldconfig, then verify the result so the installer never reports success
	# while libfaiss.so remains unresolvable.
	local ldconfig_bin
	if ! ldconfig_bin="$(find_ldconfig)"; then
		cat >&2 <<EOF

Warning: ldconfig was not found, so $LIB_DIR could not be registered with the
dynamic linker. If "$APP_NAME" fails with "libfaiss.so: cannot open shared
object file", run it with:

  LD_LIBRARY_PATH=$LIB_DIR $BIN_DIR/$APP_NAME

EOF
		return
	fi

	$SUDO install -d /etc/ld.so.conf.d
	echo "$LIB_DIR" | $SUDO tee /etc/ld.so.conf.d/$APP_NAME.conf >/dev/null
	$SUDO "$ldconfig_bin"

	if "$ldconfig_bin" -p | grep -q '\blibfaiss\.so\b' && \
		"$ldconfig_bin" -p | grep -q '\blibfaiss_c\.so\b'; then
		return
	fi

	# The conf entry should have made these resolvable. If the cache still does
	# not list them (e.g. an unusual ldconfig setup), fail loudly with the exact
	# remediation instead of leaving a broken install behind.
	cat >&2 <<EOF

ShibuDB build completed, but the FAISS libraries in $LIB_DIR are not yet
resolvable by the dynamic linker. The following files were installed:

$(ls -l "$LIB_DIR"/libfaiss.so "$LIB_DIR"/libfaiss_c.so 2>&1)

Try the following and rerun "$APP_NAME":

  echo $LIB_DIR | sudo tee /etc/ld.so.conf.d/$APP_NAME.conf
  sudo ldconfig
  ldconfig -p | grep libfaiss

If that still does not work, run with an explicit library path:

  LD_LIBRARY_PATH=$LIB_DIR $BIN_DIR/$APP_NAME

EOF
	exit 1
}

check_faiss_runtime_compatibility() {
	local faiss_lib_dir="$1"

	echo "Checking bundled FAISS runtime compatibility..."
	if ! ldd "$faiss_lib_dir/libfaiss.so"; then
		cat >&2 <<EOF

Bundled libfaiss.so is not compatible with this Linux runtime.
Use FAISS libraries built on this distro, or on an older compatible baseline.
For one shared Linux artifact, build FAISS on the oldest distro you support.

EOF
		exit 1
	fi

	if ! ldd "$faiss_lib_dir/libfaiss_c.so"; then
		cat >&2 <<EOF

Bundled libfaiss_c.so is not compatible with this Linux runtime.
Use FAISS libraries built on this distro, or on an older compatible baseline.

EOF
		exit 1
	fi
}

build_shibudb() {
	local faiss_lib_dir="$SOURCE_DIR/resources/lib/linux/$LIB_ARCH"
	local version build_time rpath_flags openblas_ldflag

	cd "$SOURCE_DIR"
	if [[ -x "./scripts/get_version.sh" ]]; then
		version="$(./scripts/get_version.sh)"
	elif [[ "$VERSION" == "latest" || "$VERSION" == "main" ]]; then
		version="$VERSION"
	else
		version="${VERSION#v}"
	fi
	build_time="$(date -u '+%Y-%m-%d_%H:%M:%S')"
	rpath_flags="-Wl,-rpath,\$ORIGIN/../lib -Wl,-rpath,$LIB_DIR -Wl,-rpath-link,$faiss_lib_dir"
	check_faiss_runtime_compatibility "$faiss_lib_dir"
	openblas_ldflag=""
	if printf 'int main(void){return 0;}\n' | gcc -x c - -lopenblas -o "$WORK_DIR/openblas-link-test" >/dev/null 2>&1; then
		openblas_ldflag="-lopenblas"
	else
		echo "OpenBLAS development symlink not found; relying on bundled FAISS runtime dependency."
	fi

	echo "Building ShibuDB $version for linux/$LIB_ARCH..."
	CGO_ENABLED=1 \
	CGO_CFLAGS="-I$SOURCE_DIR/resources/lib/include" \
	CGO_CXXFLAGS="-I$SOURCE_DIR/resources/lib/include" \
	CGO_LDFLAGS="-L$faiss_lib_dir -lfaiss -lfaiss_c -lstdc++ -lm -lgomp $openblas_ldflag $rpath_flags" \
		go build -tags faiss \
		-buildvcs=false \
		-ldflags "-s -w -X main.Version=$version -X main.BuildTime=$build_time" \
		-o "$WORK_DIR/$APP_NAME" .
}

WORK_DIR="$(mktemp -d)"
cleanup() {
	if [[ "$KEEP_BUILD_DIR" == "1" ]]; then
		echo "Kept build directory: $WORK_DIR"
	else
		rm -rf "$WORK_DIR"
	fi
}
trap cleanup EXIT

install_deps
need_cmd curl
need_cmd tar
prepare_go
prepare_source
build_shibudb
install_files

echo
echo "ShibuDB installed successfully:"
echo "  Binary: $BIN_DIR/$APP_NAME"
echo "  FAISS libraries: $LIB_DIR"
echo
"$BIN_DIR/$APP_NAME" --version

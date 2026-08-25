#!/usr/bin/env bash
set -euo pipefail

APP_NAME="shibudb"
REPO_URL="${SHIBUDB_REPO_URL:-https://github.com/shibudb-org/shibudb-server}"
VERSION="${SHIBUDB_VERSION:-latest}"
PREFIX="${SHIBUDB_PREFIX:-/usr/local}"
SKIP_DEPS="${SHIBUDB_SKIP_DEPS:-0}"
KEEP_BUILD_DIR="${SHIBUDB_KEEP_BUILD_DIR:-0}"
GO_VERSION="${SHIBUDB_GO_VERSION:-1.24.0}"
# 1 (default) builds FlatMeta GPU library when CUDA toolkit is present.
# 0 skips building/installing libshibudb_gpudist.so.
WITH_CUDA="${SHIBUDB_WITH_CUDA:-1}"

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
  --without-cuda         Do not build/install FlatMeta GPU library (CPU only).
  -h, --help             Show this help.

The binary always includes FlatMeta GPU support. When an NVIDIA GPU/driver is
present, this installer installs a CUDA toolkit <= the driver CUDA version
(from nvidia-smi), builds libshibudb_gpudist.so, and validates with
"shibudb check-gpu". At runtime ShibuDB uses the GPU if that library and a
CUDA device are present; otherwise CPU.

Environment:
  SHIBUDB_VERSION        Same as --version.
  SHIBUDB_PREFIX         Same as --prefix.
  SHIBUDB_REPO_URL       Repository URL. Defaults to $REPO_URL.
  SHIBUDB_SKIP_DEPS=1    Same as --skip-deps.
  SHIBUDB_KEEP_BUILD_DIR=1
  SHIBUDB_GO_VERSION     Go version used if a suitable go command is not found.
  SHIBUDB_WITH_CUDA=0    Same as --without-cuda.
  SHIBUDB_NVCC           Absolute path to nvcc (must be <= driver CUDA version).
  SHIBUDB_CUDA_ARCH      nvcc arch flags (default: -arch=native).
EOF
}

SOURCE_DIR=""
CUDA_ENABLED=0
CUDA_LIB_DIR=""
GPUDIST_LIB=""
NVCC_BIN=""
DRIVER_CUDA=""

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
		--with-cuda)
			# Accepted for compatibility; GPU library install is already the default.
			WITH_CUDA=1
			shift
			;;
		--without-cuda)
			WITH_CUDA=0
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
		# FlatMeta GPU library needs nvcc. Try distro packages first, then
		# NVIDIA's official CUDA apt repository (common on GCP/minimal images).
		if should_install_cuda_toolkit; then
			install_cuda_toolkit_apt
		fi
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
		if should_install_cuda_toolkit; then
			echo "Installing CUDA toolkit (nvcc) for FlatMeta GPU library..."
			$SUDO dnf install -y cuda-nvcc cuda-cudart-devel || \
				$SUDO dnf install -y nvidia-cuda-toolkit || \
				echo "Warning: could not install CUDA toolkit from dnf; install it manually if you need GPU scoring." >&2
		fi
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
		if should_install_cuda_toolkit; then
			echo "Installing CUDA toolkit (nvcc) for FlatMeta GPU library..."
			$SUDO yum install -y cuda-nvcc cuda-cudart-devel || \
				echo "Warning: could not install CUDA toolkit from yum; install it manually if you need GPU scoring." >&2
		fi
	else
		echo "Unsupported Linux package manager. Install build tools, Go, OpenBLAS, libgomp, and libstdc++ manually, then rerun with --skip-deps." >&2
		exit 1
	fi
}

version_le() {
	# Returns success if $1 <= $2 (dotted versions like 12.4).
	[[ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -n1)" == "$1" ]]
}

driver_cuda_version() {
	if ! command -v nvidia-smi >/dev/null 2>&1; then
		return 1
	fi
	nvidia-smi 2>/dev/null | sed -n 's/.*CUDA Version: \([0-9]\+\.[0-9]\+\).*/\1/p' | head -n1
}

nvcc_release_version() {
	local bin="$1"
	[[ -x "$bin" ]] || return 1
	"$bin" --version 2>/dev/null | sed -n 's/.*release \([0-9]\+\.[0-9]\+\).*/\1/p' | head -n1
}

nvcc_compatible_with_driver() {
	local bin="$1"
	local rel driver
	rel="$(nvcc_release_version "$bin" || true)"
	[[ -n "$rel" ]] || return 1
	driver="$(driver_cuda_version || true)"
	# No driver report (CPU-only / no nvidia-smi): accept any nvcc for build.
	if [[ -z "$driver" ]]; then
		return 0
	fi
	version_le "$rel" "$driver"
}

has_nvidia_gpu() {
	if command -v nvidia-smi >/dev/null 2>&1; then
		nvidia-smi -L >/dev/null 2>&1 && return 0
	fi
	[[ -e /dev/nvidia0 ]] && return 0
	return 1
}

# Resolve an nvcc that is <= the driver CUDA version. Sets NVCC_BIN and prepends
# its directory to PATH. Prefers versioned /usr/local/cuda-X.Y installs over a
# newer /usr/local/cuda symlink (avoids CUDA error 35 at runtime).
select_compatible_nvcc() {
	NVCC_BIN=""
	local driver candidate rel best_bin best_rel
	driver="$(driver_cuda_version || true)"
	DRIVER_CUDA="$driver"

	if [[ -n "${SHIBUDB_NVCC:-}" ]]; then
		if [[ -x "$SHIBUDB_NVCC" ]] && nvcc_compatible_with_driver "$SHIBUDB_NVCC"; then
			NVCC_BIN="$SHIBUDB_NVCC"
			export PATH="$(dirname "$NVCC_BIN"):$PATH"
			return 0
		fi
		echo "Warning: SHIBUDB_NVCC=$SHIBUDB_NVCC is missing or newer than driver CUDA ${driver:-unknown}; ignoring." >&2
	fi

	best_bin=""
	best_rel=""
	# Prefer exact driver-matched toolkit paths first.
	if [[ -n "$driver" ]]; then
		for candidate in \
			"/usr/local/cuda-${driver}/bin/nvcc" \
			"/usr/local/cuda-${driver/./-}/bin/nvcc"; do
			if [[ -x "$candidate" ]] && nvcc_compatible_with_driver "$candidate"; then
				NVCC_BIN="$candidate"
				export PATH="$(dirname "$NVCC_BIN"):$PATH"
				return 0
			fi
		done
	fi

	# Scan installed toolkits; pick highest release still <= driver.
	local found=()
	while IFS= read -r candidate; do
		found+=("$candidate")
	done < <(ls -1d /usr/local/cuda-*/bin/nvcc 2>/dev/null || true)
	for candidate in \
		"${found[@]}" \
		/usr/local/cuda/bin/nvcc \
		/usr/lib/nvidia-cuda-toolkit/bin/nvcc \
		/usr/bin/nvcc; do
		[[ -x "$candidate" ]] || continue
		rel="$(nvcc_release_version "$candidate" || true)"
		[[ -n "$rel" ]] || continue
		if [[ -n "$driver" ]] && ! version_le "$rel" "$driver"; then
			echo "Ignoring nvcc at $candidate (release $rel > driver CUDA $driver)."
			continue
		fi
		# Keep the newest compatible toolkit.
		if [[ -z "$best_rel" ]] || version_le "$best_rel" "$rel"; then
			best_bin="$candidate"
			best_rel="$rel"
		fi
	done

	if [[ -n "$best_bin" ]]; then
		NVCC_BIN="$best_bin"
		export PATH="$(dirname "$NVCC_BIN"):$PATH"
		return 0
	fi

	# PATH nvcc as last resort when no driver constraint.
	if command -v nvcc >/dev/null 2>&1; then
		candidate="$(command -v nvcc)"
		if nvcc_compatible_with_driver "$candidate"; then
			NVCC_BIN="$candidate"
			return 0
		fi
		rel="$(nvcc_release_version "$candidate" || true)"
		echo "PATH nvcc ($candidate, release ${rel:-unknown}) is newer than driver CUDA ${driver:-unknown}."
	fi
	return 1
}

should_install_cuda_toolkit() {
	case "$WITH_CUDA" in
		0|false|no|off) return 1 ;;
	esac
	# Already have a driver-compatible nvcc.
	if select_compatible_nvcc; then
		return 1
	fi
	# Install toolkit when an NVIDIA GPU/driver is present, or user forced CUDA.
	if has_nvidia_gpu; then
		return 0
	fi
	case "$WITH_CUDA" in
		1|true|yes|on) return 0 ;;
	esac
	return 1
}

cuda_repo_distro_arch() {
	# Prints "distro arch" for NVIDIA CUDA apt repos, e.g. "debian12 x86_64".
	local id version_id arch repo_distro
	if [[ ! -f /etc/os-release ]]; then
		return 1
	fi
	# shellcheck disable=SC1091
	. /etc/os-release
	id="${ID:-}"
	version_id="${VERSION_ID:-}"
	case "$(uname -m)" in
		x86_64) arch="x86_64" ;;
		aarch64|arm64) arch="sbsa" ;;
		*) return 1 ;;
	esac

	case "$id" in
		debian)
			case "$version_id" in
				12*|13*) repo_distro="debian12" ;;
				11*) repo_distro="debian11" ;;
				*) return 1 ;;
			esac
			;;
		ubuntu)
			case "$version_id" in
				24.04*|24.10*|25.*) repo_distro="ubuntu2404" ;;
				22.04*) repo_distro="ubuntu2204" ;;
				20.04*) repo_distro="ubuntu2004" ;;
				*) return 1 ;;
			esac
			if [[ "$arch" == "sbsa" ]]; then
				arch="arm64"
			fi
			;;
		*)
			return 1
			;;
	esac
	echo "$repo_distro $arch"
}

# Install nvcc + cudart headers/libs via apt.
# Always prefers toolkit packages <= nvidia-smi "CUDA Version" (avoids error 35).
# GCP/minimal images lack nvidia-cuda-toolkit; use NVIDIA cuda-keyring instead.
install_cuda_toolkit_apt() {
	echo "Installing CUDA toolkit (nvcc) for FlatMeta GPU library..."

	if select_compatible_nvcc; then
		echo "Compatible CUDA toolkit already present: $NVCC_BIN ($(nvcc_release_version "$NVCC_BIN"))"
		return 0
	fi

	local driver_cuda
	driver_cuda="$(driver_cuda_version || true)"
	DRIVER_CUDA="$driver_cuda"
	if [[ -n "$driver_cuda" ]]; then
		echo "NVIDIA driver reports CUDA Version: $driver_cuda (toolkit must be <= this)."
	fi

	# Prefer NVIDIA versioned packages matching the driver (reliable on GCP).
	if install_nvidia_cuda_repo_apt; then
		if install_cuda_nvcc_packages_apt "$driver_cuda"; then
			return 0
		fi
	fi

	# Distro packages (often missing on minimal GCP images).
	echo "Trying distro nvidia-cuda-toolkit packages..."
	if $SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y \
		nvidia-cuda-toolkit \
		nvidia-cuda-dev 2>/dev/null; then
		if select_compatible_nvcc; then
			echo "Installed distro CUDA toolkit: $NVCC_BIN ($(nvcc_release_version "$NVCC_BIN"))"
			return 0
		fi
		echo "Distro nvidia-cuda-toolkit is newer than driver CUDA ${driver_cuda:-unknown}; ignoring for FlatMeta GPU build."
	fi

	print_cuda_manual_install_help
	return 1
}

install_cuda_nvcc_packages_apt() {
	local driver_cuda="${1:-}"
	local candidates=()
	mapfile -t candidates < <(apt-cache search --names-only '^cuda-nvcc-[0-9]+-[0-9]+$' 2>/dev/null | awk '{print $1}' | sort -V)
	if [[ ${#candidates[@]} -eq 0 ]]; then
		candidates=(cuda-nvcc-12-8 cuda-nvcc-12-6 cuda-nvcc-12-4 cuda-nvcc-12-2 cuda-nvcc-11-8)
	fi
	if [[ -n "$driver_cuda" ]]; then
		echo "Selecting CUDA toolkit packages <= driver CUDA $driver_cuda ..."
		local filtered=()
		local pkg ver
		for pkg in "${candidates[@]}"; do
			ver="${pkg#cuda-nvcc-}"
			ver="${ver/-/.}"
			if version_le "$ver" "$driver_cuda"; then
				filtered+=("$pkg")
			fi
		done
		if [[ ${#filtered[@]} -eq 0 ]]; then
			echo "No cuda-nvcc packages <= driver CUDA $driver_cuda found in apt." >&2
			return 1
		fi
		candidates=("${filtered[@]}")
	fi

	# Try newest compatible first.
	local nvcc_pkg cudart_pkg i ver_dir
	for ((i=${#candidates[@]}-1; i>=0; i--)); do
		nvcc_pkg="${candidates[$i]}"
		cudart_pkg="${nvcc_pkg/cuda-nvcc-/cuda-cudart-dev-}"
		echo "Trying $nvcc_pkg + $cudart_pkg ..."
		if $SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y "$nvcc_pkg" "$cudart_pkg"; then
			ver_dir="${nvcc_pkg#cuda-nvcc-}"
			ver_dir="${ver_dir/-/.}"
			export PATH="/usr/local/cuda-${ver_dir}/bin:/usr/local/cuda/bin:$PATH"
			if select_compatible_nvcc; then
				echo "Installed CUDA toolkit via NVIDIA repo: $NVCC_BIN ($(nvcc_release_version "$NVCC_BIN"))"
				return 0
			fi
		fi
	done

	# Last resort: toolkit meta package matching driver when possible.
	local meta_pkgs=()
	if [[ -n "$driver_cuda" ]]; then
		meta_pkgs+=("cuda-toolkit-${driver_cuda/./-}")
	fi
	meta_pkgs+=(cuda-toolkit-12-4 cuda-toolkit-12-2 cuda-toolkit-11-8 cuda-toolkit)
	local meta
	for meta in "${meta_pkgs[@]}"; do
		echo "Trying meta package $meta ..."
		if $SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y "$meta"; then
			export PATH="/usr/local/cuda/bin:$PATH"
			if select_compatible_nvcc; then
				echo "Installed CUDA toolkit meta package: $NVCC_BIN ($(nvcc_release_version "$NVCC_BIN"))"
				return 0
			fi
		fi
	done
	return 1
}

install_nvidia_cuda_repo_apt() {
	local repo_distro arch keyring_url keyring_deb dest
	local parsed
	if ! parsed="$(cuda_repo_distro_arch)"; then
		echo "NVIDIA CUDA apt repo auto-setup supports Debian 11/12 and Ubuntu 20.04/22.04/24.04." >&2
		return 1
	fi
	repo_distro="${parsed%% *}"
	arch="${parsed##* }"

	# Already configured?
	if [[ -f /etc/apt/sources.list.d/cuda-debian12-x86_64.list ]] || \
		[[ -f /etc/apt/sources.list.d/cuda-ubuntu2204-x86_64.list ]] || \
		[[ -f /etc/apt/sources.list.d/cuda-${repo_distro}-${arch}.list ]] || \
		compgen -G "/etc/apt/sources.list.d/cuda*.list" >/dev/null 2>&1; then
		echo "NVIDIA CUDA apt repository already present."
		$SUDO apt-get update || true
		return 0
	fi

	keyring_deb="cuda-keyring_1.1-1_all.deb"
	keyring_url="https://developer.download.nvidia.com/compute/cuda/repos/${repo_distro}/${arch}/${keyring_deb}"
	dest="${WORK_DIR:-/tmp}/$keyring_deb"
	echo "Adding NVIDIA CUDA apt repo ($repo_distro/$arch)..."
	if ! curl -fsSL "$keyring_url" -o "$dest"; then
		if [[ "$arch" == "arm64" || "$arch" == "sbsa" ]]; then
			keyring_url="https://developer.download.nvidia.com/compute/cuda/repos/${repo_distro}/arm64/${keyring_deb}"
			curl -fsSL "$keyring_url" -o "$dest" || return 1
		else
			echo "Failed to download $keyring_url" >&2
			return 1
		fi
	fi
	$SUDO dpkg -i "$dest"
	$SUDO apt-get update
	return 0
}

print_cuda_manual_install_help() {
	local driver_cuda repo_hint pkg_ver
	driver_cuda="$(driver_cuda_version || true)"
	[[ -n "$driver_cuda" ]] || driver_cuda="12.4"
	pkg_ver="${driver_cuda/./-}"
	repo_hint="debian12/x86_64"
	local parsed
	if parsed="$(cuda_repo_distro_arch 2>/dev/null)"; then
		repo_hint="${parsed%% *}/${parsed##* }"
	fi
	cat >&2 <<EOF
Warning: could not install a CUDA toolkit compatible with driver CUDA $driver_cuda.

Install a matching toolkit (NOT newer than the driver), then rebuild:

  curl -fsSL -o /tmp/cuda-keyring.deb \\
    https://developer.download.nvidia.com/compute/cuda/repos/${repo_hint}/cuda-keyring_1.1-1_all.deb
  sudo dpkg -i /tmp/cuda-keyring.deb
  sudo apt-get update
  sudo apt-get install -y cuda-nvcc-${pkg_ver} cuda-cudart-dev-${pkg_ver}
  export PATH=/usr/local/cuda-${driver_cuda}/bin:/usr/local/cuda/bin:\$PATH
  nvcc --version

  make build-gpudist-cuda
  sudo install -m 0755 internal/storage/gpudist/cuda/libshibudb_gpudist.so $LIB_DIR/
  sudo ldconfig
  shibudb check-gpu

EOF
}

version_at_least() {
	# Returns success if $1 >= $2.
	[[ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -n1)" == "$2" ]]
}

prepare_go() {
	if command -v go >/dev/null 2>&1; then
		local current
		current="$(go env GOVERSION 2>/dev/null | sed 's/^go//')"
		if [[ -n "$current" ]] && version_at_least "$current" "1.24.0"; then
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

find_cuda_lib_dir() {
	local candidate driver
	driver="$(driver_cuda_version || true)"
	local candidates=()
	if [[ -n "$driver" ]]; then
		candidates+=(
			"/usr/local/cuda-${driver}/lib64"
			"/usr/local/cuda-${driver/./-}/lib64"
		)
	fi
	candidates+=(
		"${CUDA_HOME:-}/lib64"
		"${CUDA_PATH:-}/lib64"
		/usr/local/cuda/lib64
		/usr/lib/x86_64-linux-gnu
		/usr/lib/aarch64-linux-gnu
		/usr/lib/nvidia-cuda-toolkit/lib
		/usr/lib64
		/usr/lib
	)
	# Also scan versioned CUDA installs.
	while IFS= read -r candidate; do
		candidates+=("$candidate")
	done < <(ls -1d /usr/local/cuda-*/lib64 2>/dev/null || true)

	for candidate in "${candidates[@]}"; do
		[[ -n "$candidate" ]] || continue
		if [[ -e "$candidate/libcudart.so" || -e "$candidate/libcudart.so.12" || -e "$candidate/libcudart.so.11" ]]; then
			echo "$candidate"
			return 0
		fi
	done
	# Debian's nvidia-cuda-toolkit often ships versioned sonames only.
	local match
	match="$(ls /usr/lib/x86_64-linux-gnu/libcudart.so* 2>/dev/null | head -n1 || true)"
	if [[ -n "$match" ]]; then
		echo "$(dirname "$match")"
		return 0
	fi
	match="$(ls /usr/lib/aarch64-linux-gnu/libcudart.so* 2>/dev/null | head -n1 || true)"
	if [[ -n "$match" ]]; then
		echo "$(dirname "$match")"
		return 0
	fi
	return 1
}

detect_cuda() {
	CUDA_ENABLED=0
	CUDA_LIB_DIR=""
	GPUDIST_LIB=""
	NVCC_BIN=""

	case "$WITH_CUDA" in
		0|false|no|off)
			echo "Skipping FlatMeta GPU library build (--without-cuda)."
			return 0
			;;
	esac

	# If deps were skipped but a compatible toolkit is missing, try installing it
	# when an NVIDIA GPU is present (common on GCP after a partial install).
	if ! select_compatible_nvcc; then
		if has_nvidia_gpu && command -v apt-get >/dev/null 2>&1; then
			echo "No driver-compatible nvcc found; attempting CUDA toolkit install..."
			install_cuda_toolkit_apt || true
		fi
	fi

	if ! select_compatible_nvcc; then
		if has_nvidia_gpu; then
			print_cuda_manual_install_help
		else
			echo "nvcc not found and no NVIDIA GPU detected; installing without libshibudb_gpudist.so."
		fi
		return 0
	fi

	if ! CUDA_LIB_DIR="$(find_cuda_lib_dir)"; then
		echo "CUDA runtime library (libcudart) not found; installing without libshibudb_gpudist.so."
		return 0
	fi

	CUDA_ENABLED=1
	local nvcc_rel
	nvcc_rel="$(nvcc_release_version "$NVCC_BIN")"
	echo "CUDA toolkit OK (nvcc=$NVCC_BIN release=$nvcc_rel, driver_cuda=${DRIVER_CUDA:-unknown}, cudart=$CUDA_LIB_DIR)"
	echo "Building FlatMeta GPU library with this toolkit (must be <= driver to avoid CUDA error 35)."
}

build_gpudist_cuda() {
	local cuda_dir="$SOURCE_DIR/internal/storage/gpudist/cuda"
	local out="$cuda_dir/libshibudb_gpudist.so"
	local build_script="$SOURCE_DIR/scripts/build-gpudist-cuda.sh"

	if [[ ! -f "$cuda_dir/distances.cu" ]]; then
		echo "Missing FlatMeta CUDA sources at $cuda_dir/distances.cu" >&2
		exit 1
	fi

	echo "Building FlatMeta GPU distance library..."
	# Prefer the shared build script (driver/nvcc version checks).
	if [[ -x "$build_script" ]] || [[ -f "$build_script" ]]; then
		chmod +x "$build_script" 2>/dev/null || true
		if ! SHIBUDB_NVCC="$NVCC_BIN" "$build_script"; then
			echo "Failed to build FlatMeta GPU library with $NVCC_BIN" >&2
			exit 1
		fi
	else
		local arch_flags="${SHIBUDB_CUDA_ARCH:--arch=native}"
		# shellcheck disable=SC2086
		if ! "$NVCC_BIN" -shared -Xcompiler -fPIC -O3 $arch_flags \
			-o "$out" \
			"$cuda_dir/distances.cu"; then
			echo "nvcc -arch=native failed; retrying with sm_75 (Turing / T4)..."
			"$NVCC_BIN" -shared -Xcompiler -fPIC -O3 -gencode arch=compute_75,code=sm_75 \
				-o "$out" \
				"$cuda_dir/distances.cu"
		fi
	fi

	if [[ ! -f "$out" ]]; then
		echo "FlatMeta GPU library was not produced at $out" >&2
		exit 1
	fi

	# Validate the .so links against cudart and report which one.
	echo "GPU library link check:"
	ldd "$out" | grep -E 'cudart|not found' || true
	if ldd "$out" 2>/dev/null | grep -q 'not found'; then
		echo "error: libshibudb_gpudist.so has unresolved libraries" >&2
		ldd "$out" >&2 || true
		exit 1
	fi
	GPUDIST_LIB="$out"
}

validate_gpu_runtime() {
	local bin="$BIN_DIR/$APP_NAME"
	local lib="$LIB_DIR/libshibudb_gpudist.so"
	local json

	case "$WITH_CUDA" in
		0|false|no|off) return 0 ;;
	esac

	if [[ "$CUDA_ENABLED" != "1" || ! -f "$lib" ]]; then
		return 0
	fi

	echo
	echo "Validating FlatMeta GPU runtime (shibudb check-gpu)..."
	json="$("$bin" check-gpu --json 2>/dev/null || true)"
	if [[ -z "$json" ]]; then
		# Older binaries without --json: best-effort.
		if "$bin" check-gpu >/dev/null 2>&1; then
			echo "FlatMeta GPU validation: ready"
			return 0
		fi
		echo "Warning: could not run '$bin check-gpu'; library installed at $lib." >&2
		return 0
	fi

	echo "$json"
	if echo "$json" | grep -q '"ready"[[:space:]]*:[[:space:]]*true'; then
		echo "FlatMeta GPU validation: ready"
		return 0
	fi

	cat >&2 <<EOF

Warning: libshibudb_gpudist.so is installed, but check-gpu reports not ready.
Common cause: toolkit newer than the NVIDIA driver (CUDA error 35).
  nvidia-smi   # note "CUDA Version"
  ldd $lib | grep cudart
  # Rebuild with toolkit <= driver CUDA version, e.g. cuda-nvcc-12-4

EOF
	# Do not fail the overall install: CPU fallback still works.
	return 0
}

install_files() {
	local build_bin="$WORK_DIR/$APP_NAME"
	local faiss_lib_dir="$SOURCE_DIR/resources/lib/linux/$LIB_ARCH"

	$SUDO install -d "$BIN_DIR" "$LIB_DIR" "$SHARE_DIR"
	$SUDO install -m 0755 "$build_bin" "$BIN_DIR/$APP_NAME"
	$SUDO install -m 0755 "$faiss_lib_dir/libfaiss.so" "$LIB_DIR/libfaiss.so"
	$SUDO install -m 0755 "$faiss_lib_dir/libfaiss_c.so" "$LIB_DIR/libfaiss_c.so"
	if [[ "$CUDA_ENABLED" == "1" && -n "$GPUDIST_LIB" && -f "$GPUDIST_LIB" ]]; then
		$SUDO install -m 0755 "$GPUDIST_LIB" "$LIB_DIR/libshibudb_gpudist.so"
	fi
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
		if [[ "$CUDA_ENABLED" == "1" ]]; then
			if ! "$ldconfig_bin" -p | grep -q '\blibshibudb_gpudist\.so\b'; then
				echo "Warning: libshibudb_gpudist.so is installed in $LIB_DIR but not yet visible to ldconfig." >&2
			fi
		fi
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

	# Always build/install the FlatMeta GPU library when CUDA toolkit is present.
	# The binary loads it via dlopen at runtime and falls back to CPU if missing.
	if [[ "$CUDA_ENABLED" == "1" ]]; then
		build_gpudist_cuda
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
detect_cuda
build_shibudb
install_files
validate_gpu_runtime

echo
echo "ShibuDB installed successfully:"
echo "  Binary: $BIN_DIR/$APP_NAME"
echo "  FAISS libraries: $LIB_DIR"
if [[ "$CUDA_ENABLED" == "1" ]]; then
	echo "  FlatMeta GPU library: installed ($LIB_DIR/libshibudb_gpudist.so)"
	echo "  CUDA nvcc used: $NVCC_BIN ($(nvcc_release_version "$NVCC_BIN" 2>/dev/null || echo unknown))"
	if [[ -n "${DRIVER_CUDA:-}" ]]; then
		echo "  NVIDIA driver CUDA Version: $DRIVER_CUDA"
	fi
	echo "  Verify anytime: $BIN_DIR/$APP_NAME check-gpu --json"
else
	echo "  FlatMeta GPU library: not installed (CPU scoring; add lib later if needed)"
fi
echo
"$BIN_DIR/$APP_NAME" --version

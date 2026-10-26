#!/usr/bin/env bash
set -euo pipefail
exec 3>&1
exec 1>&2

usage() { echo "usage: prepare-linux.sh --config <ci-tools.json> --plan <plan.json> --platform <linux-x86_64|linux-aarch64> --work-root <directory>" >&2; }
config=
plan=
platform=
work_root=
while (($#)); do
  case "$1" in
    --config) (($# >= 2)) || { usage; exit 2; }; config=$2; shift 2 ;;
    --plan) (($# >= 2)) || { usage; exit 2; }; plan=$2; shift 2 ;;
    --platform) (($# >= 2)) || { usage; exit 2; }; platform=$2; shift 2 ;;
    --work-root) (($# >= 2)) || { usage; exit 2; }; work_root=$2; shift 2 ;;
    *) usage; exit 2 ;;
  esac
done
[[ -n "$config" && -n "$plan" && -n "$platform" && -n "$work_root" ]] || { usage; exit 2; }
[[ -f "$config" && -f "$plan" ]] || { echo "Config and plan files must exist." >&2; exit 2; }
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
config=$(realpath "$config")
plan=$(realpath "$plan")
mkdir -p "$work_root"
work_root=$(realpath "$work_root")
tools="$work_root/ci-tools"
mkdir -p "$tools/bin" "$tools/src"

arch=$(dpkg --print-architecture)
case "$arch:$platform" in
  amd64:linux-x86_64) apt_arch=amd64; archive_host=archive.ubuntu.com/ubuntu; security_host=security.ubuntu.com/ubuntu ;;
  arm64:linux-aarch64) apt_arch=arm64; archive_host=ports.ubuntu.com/ubuntu-ports; security_host=ports.ubuntu.com/ubuntu-ports ;;
  *) echo "Container architecture $arch does not match requested platform $platform." >&2; exit 1 ;;
esac
gcc_version=$(jq -er '.production.linux.gcc_version' "$config")
[[ "$gcc_version" =~ ^9\.[0-9]+$ ]] || { echo "Unsupported configured GCC version: $gcc_version" >&2; exit 1; }
gcc_major=${gcc_version%%.*}
cat >/etc/apt/sources.list <<EOF
deb [arch=$apt_arch] http://$archive_host focal main universe multiverse restricted
deb [arch=$apt_arch] http://$archive_host focal-updates main universe multiverse restricted
deb [arch=$apt_arch] http://$security_host focal-security main universe multiverse restricted
EOF
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends ca-certificates curl git jq build-essential "gcc-$gcc_major" "g++-$gcc_major" pkg-config libgomp1 xz-utils tar unzip

cmake_version=$(jq -er '.cmake.version' "$config")
cmake_file=$(jq -er --arg platform "$platform" '.cmake.assets[] | select(.platform == $platform) | .filename' "$config")
cmake_url=$(jq -er --arg platform "$platform" '.cmake.assets[] | select(.platform == $platform) | .url' "$config")
cmake_hash=$(jq -er --arg platform "$platform" '.cmake.assets[] | select(.platform == $platform) | .sha256' "$config")
cmake_archive="$tools/src/$cmake_file"
curl --fail --location --retry 2 "$cmake_url" -o "$cmake_archive"
echo "$cmake_hash  $cmake_archive" | sha256sum --check
tar -xzf "$cmake_archive" --strip-components=1 -C "$tools"
cmake_command="$tools/bin/cmake"
[[ "$("$cmake_command" --version | head -n1)" == "cmake version $cmake_version" ]] || { echo "Unexpected CMake version." >&2; exit 1; }

make_version=$(jq -er '.make.version' "$config")
make_url=$(jq -er '.make.url' "$config")
make_hash=$(jq -er '.make.sha256' "$config")
make_archive="$tools/src/make-$make_version.tar.gz"
curl --fail --location --retry 2 "$make_url" -o "$make_archive"
echo "$make_hash  $make_archive" | sha256sum --check
tar -xzf "$make_archive" -C "$tools/src"
make_source="$tools/src/make-$make_version"
(
  cd "$make_source"
  ./configure --prefix="$tools"
)
make -C "$make_source"
make -C "$make_source" install

actual_gcc_version=$(gcc-"$gcc_major" -dumpfullversion)
actual_gxx_version=$(g++-"$gcc_major" -dumpfullversion)
[[ "$actual_gcc_version" == "$gcc_version".* && "$actual_gxx_version" == "$gcc_version".* ]] || { echo "Configured GCC/G++ $gcc_version does not match installed $actual_gcc_version/$actual_gxx_version." >&2; exit 1; }
[[ "$("$tools/bin/make" --version | head -n1)" == "GNU Make $make_version" ]] || { echo "Installed Make does not match configuration." >&2; exit 1; }

if ! jq -e --arg platform "$platform" '[.platforms[] | select(.id == $platform)] | length == 1' "$plan" >/dev/null; then
  echo "Plan must contain exactly one platform entry for $platform." >&2
  exit 1
fi
mapfile -t apt_packages < <(jq -r --arg platform "$platform" '[.platforms[] | select(.id == $platform) | .system_dependencies.apt[]?] | unique | .[]' "$plan")
if ((${#apt_packages[@]})); then
  for package in "${apt_packages[@]}"; do [[ "$package" =~ ^[a-z0-9][a-z0-9+.-]*$ ]] || { echo "Unsafe APT package name: $package" >&2; exit 1; }; done
  apt-get install -y --no-install-recommends "${apt_packages[@]}"
fi

export PATH="$tools/bin:$PATH" CC="gcc-$gcc_major" CXX="g++-$gcc_major"
git config --global --add safe.directory "$repo_root"
"$cmake_command" -P "$repo_root/src/autolib-builder/go-sdk.cmake" -- build -mod=readonly -trimpath -o "$work_root/autolib-builder" ./src/autolib-builder
"$cmake_command" -P "$repo_root/src/autolib-builder/go-sdk.cmake" -- build -mod=readonly -trimpath -o "$work_root/autolib-ci" ./src/autolib-ci
if [[ -n "${GITHUB_PATH:-}" ]]; then printf '%s\n' "$tools/bin" >>"$GITHUB_PATH"; fi
if [[ -n "${GITHUB_ENV:-}" ]]; then printf 'CC=gcc-%s\nCXX=g++-%s\n' "$gcc_major" "$gcc_major" >>"$GITHUB_ENV"; fi
dpkg-query -W -f='${binary:Package}=${Version}\n' ca-certificates curl git jq build-essential "gcc-$gcc_major" "g++-$gcc_major" pkg-config libgomp1 xz-utils tar unzip libgcc-s1 libstdc++6 "${apt_packages[@]}" 2>/dev/null | sort -u >&2
printf 'architecture=%s\nGCC=%s\nG++=%s\nMake=%s\nCMake=%s\n' "$arch" "$actual_gcc_version" "$actual_gxx_version" "$("$tools/bin/make" --version | head -n1)" "$("$cmake_command" --version | head -n1)" >&2
budget=$(nproc)
[[ "$budget" =~ ^[0-9]+$ ]] || { echo "nproc returned an invalid budget: $budget" >&2; exit 1; }
if ((budget < 1)); then budget=1; fi
jq -cn --arg cmake "$cmake_command" --arg make "$tools/bin/make" --argjson budget "$budget" '{cmake_command:$cmake,make_program:$make,budget:$budget}' >&3

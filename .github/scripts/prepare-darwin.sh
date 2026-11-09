#!/usr/bin/env bash
set -euo pipefail
exec 3>&1
exec 1>&2

usage() { echo 'usage: prepare-darwin.sh --config <ci-tools.json> --plan <plan.json> --platform darwin-aarch64 --work-root <directory>' >&2; }
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
[[ -n "$config" && -n "$plan" && "$platform" == darwin-aarch64 && -n "$work_root" ]] || { usage; exit 2; }
[[ -f "$config" && -f "$plan" ]] || { echo 'Config and plan files must exist.' >&2; exit 2; }
[[ "$(uname -s)" == Darwin && "$(uname -m)" == arm64 ]] || { echo 'Darwin preparation requires a native Darwin arm64 host.' >&2; exit 1; }
if ! jq -e --arg platform "$platform" '[.platforms[] | select(.id == $platform)] | length == 1' "$plan" >/dev/null; then
  echo "Plan must contain exactly one platform entry for $platform." >&2
  exit 1
fi
has_libraries=$(jq -r --arg platform "$platform" '.platforms[] | select(.id == $platform) | .roots | length > 0' "$plan")
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
mkdir -p "$work_root"
work_root=$(cd "$work_root" && pwd)
tools="$work_root/ci-tools"
mkdir -p "$tools/bin" "$tools/src"

formula_json=$(jq -ce --arg platform "$platform" '
  .platforms[] | select(.id == $platform) | (.system_dependencies // {}) |
  (if has("brew") then .brew else [] end) |
  if type == "array" and all(.[]; type == "string" and test("^[a-z0-9][a-z0-9+_.@-]*$"))
  then reduce .[] as $formula ([]; if index($formula) then . else . + [$formula] end)
  else error("Invalid system_dependencies.brew formula array") end' "$plan")
formulas=()
while IFS= read -r formula; do formulas+=("$formula"); done < <(jq -r '.[]' <<<"$formula_json")
if ((${#formulas[@]})); then
  HOMEBREW_NO_AUTO_UPDATE=1 brew install "${formulas[@]}"
  brew list --versions "${formulas[@]}"
  formula_prefixes=
  formula_pkgconfig=
  for formula in "${formulas[@]}"; do
    prefix=$(brew --prefix "$formula")
    formula_prefixes+="${formula_prefixes:+:}$prefix"
    formula_pkgconfig+="${formula_pkgconfig:+:}$prefix/lib/pkgconfig:$prefix/share/pkgconfig"
  done
  export CMAKE_PREFIX_PATH="$formula_prefixes${CMAKE_PREFIX_PATH:+:$CMAKE_PREFIX_PATH}"
  export PKG_CONFIG_PATH="$formula_pkgconfig${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
  if [[ -n "${GITHUB_ENV:-}" ]]; then
    printf 'CMAKE_PREFIX_PATH=%s\nPKG_CONFIG_PATH=%s\n' "$CMAKE_PREFIX_PATH" "$PKG_CONFIG_PATH" >>"$GITHUB_ENV"
  fi
fi

cmake_version=$(jq -er '.cmake.version' "$config")
cmake_file=$(jq -er --arg platform "$platform" '.cmake.assets[] | select(.platform == $platform) | .filename' "$config")
cmake_url=$(jq -er --arg platform "$platform" '.cmake.assets[] | select(.platform == $platform) | .url' "$config")
cmake_hash=$(jq -er --arg platform "$platform" '.cmake.assets[] | select(.platform == $platform) | .sha256' "$config")
cmake_archive="$tools/src/$cmake_file"
curl --fail --location --retry 2 "$cmake_url" -o "$cmake_archive"
printf '%s  %s\n' "$cmake_hash" "$cmake_archive" | shasum -a 256 --check
tar -xzf "$cmake_archive" -C "$tools"
cmake_command="$tools/${cmake_file%.tar.gz}/CMake.app/Contents/bin/cmake"
[[ "$("$cmake_command" --version | head -n1)" == "cmake version $cmake_version" ]] || { echo 'Unexpected CMake version.' >&2; exit 1; }

export CC="$(xcrun --find clang)" CXX="$(xcrun --find clang++)"
sw_vers
printf 'SDK=%s\n' "$(xcrun --show-sdk-version)"
"$CC" --version
"$CXX" --version
make_program=
budget=0
if [[ "$has_libraries" == true ]]; then
  make_version=$(jq -er '.make.version' "$config")
  make_url=$(jq -er '.make.url' "$config")
  make_hash=$(jq -er '.make.sha256' "$config")
  make_archive="$tools/src/make-$make_version.tar.gz"
  curl --fail --location --retry 2 "$make_url" -o "$make_archive"
  printf '%s  %s\n' "$make_hash" "$make_archive" | shasum -a 256 --check
  tar -xzf "$make_archive" -C "$tools/src"
  make_source="$tools/src/make-$make_version"
  bootstrap_make=$(xcrun --find make)
  SDKROOT=$(xcrun --sdk macosx --show-sdk-path)
  export SDKROOT
  printf 'Make SDKROOT=%s\n' "$SDKROOT"
  (cd "$make_source" && ./configure --prefix="$tools") || {
    status=$?
    cat "$make_source/config.log" >&2
    exit "$status"
  }
  "$bootstrap_make" -C "$make_source"
  "$bootstrap_make" -C "$make_source" install
  make_program="$tools/bin/make"
  [[ "$("$make_program" --version | head -n1)" == "GNU Make $make_version" ]] || { echo 'Unexpected installed Make version.' >&2; exit 1; }
  budget=$(sysctl -n hw.logicalcpu)
  [[ "$budget" =~ ^[0-9]+$ ]] || { echo "Invalid logical CPU budget: $budget" >&2; exit 1; }
  if ((budget < 1)); then budget=1; fi
  "$make_program" --version
fi
export PATH="$(dirname "$cmake_command"):$tools/bin:$PATH"
(cd "$repo_root" && "$cmake_command" -P "$repo_root/src/autolib-builder/go-sdk.cmake" -- build -mod=readonly -trimpath -o "$work_root/autolib-ci" ./src/autolib-ci)
if [[ -n "${GITHUB_PATH:-}" ]]; then printf '%s\n%s\n' "$(dirname "$cmake_command")" "$tools/bin" >>"$GITHUB_PATH"; fi
if [[ -n "${GITHUB_ENV:-}" ]]; then printf 'CC=%s\nCXX=%s\n' "$CC" "$CXX" >>"$GITHUB_ENV"; fi
printf 'CMake=%s\nbudget=%s\n' "$("$cmake_command" --version | head -n1)" "$budget"
jq -cn --arg cmake "$cmake_command" --arg make "$make_program" --argjson budget "$budget" '{cmake_command:$cmake,make_program:$make,budget:$budget}' >&3

#!/bin/bash
#
# go_env.sh - Discover and export the correct Go toolchain for this repo.
#
# Usage:
#   source ./scripts/go_env.sh
#   "$GO_BIN" version
#

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  echo "source this script instead of executing it directly: source ./scripts/go_env.sh" >&2
  exit 1
fi

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

required_go_version() {
  awk '/^go / { print $2; exit }' "$PROJECT_ROOT/go.mod"
}

version_gte() {
  local left="${1#go}"
  local right="${2#go}"
  [[ "$(printf '%s\n%s\n' "$right" "$left" | sort -V | head -n1)" == "$right" ]]
}

candidate_version() {
  local bin="$1"
  local version

  if [[ ! -x "$bin" ]]; then
    return 1
  fi

  version="$("$bin" version 2>/dev/null | awk '{print $3}')"
  if [[ -z "$version" ]]; then
    return 1
  fi

  printf '%s' "$version"
}

select_go_bin() {
  local required="$1"
  local version
  local -a candidates=()

  candidates+=("$HOME/go/pkg/mod/golang.org/toolchain@v0.0.1-go${required}.linux-amd64/bin/go")
  candidates+=("/usr/local/go/bin/go")

  if command -v go >/dev/null 2>&1; then
    candidates+=("$(command -v go)")
  fi

  for candidate in "${candidates[@]}"; do
    version="$(candidate_version "$candidate")" || continue
    if version_gte "$version" "$required"; then
      printf '%s' "$candidate"
      return 0
    fi
  done

  return 1
}

NOFX_REQUIRED_GO_VERSION="${NOFX_REQUIRED_GO_VERSION:-$(required_go_version)}"

GO_BIN="$(select_go_bin "$NOFX_REQUIRED_GO_VERSION")" || {
  echo "failed to find a Go toolchain >= ${NOFX_REQUIRED_GO_VERSION}" >&2
  return 1
}

GO_ROOT="$("$GO_BIN" env GOROOT 2>/dev/null)"
if [[ -z "$GO_ROOT" ]]; then
  GO_ROOT="$(cd "$(dirname "$GO_BIN")/.." && pwd)"
fi

export GO_BIN
export GOROOT="$GO_ROOT"
export PATH="$(dirname "$GO_BIN"):$PATH"
export GOCACHE="${GOCACHE:-/tmp/nofx-go-build}"


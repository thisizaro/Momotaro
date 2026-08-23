#!/usr/bin/env bash
#
# Decides which service images the docker job actually needs to rebuild, so a
# docs-only change does not rebuild nine Go binaries (AGENTS.md, "Branching
# and CI conventions").
#
# Fails safe in every ambiguous case: if shared code changed, or the diff base
# cannot be worked out, every image is built. A filter that wrongly skips a
# build is a broken image reaching main; a filter that wrongly runs one only
# costs a minute.
#
# Inputs (environment):
#   EVENT     github.event_name
#   BASE_SHA  github.event.pull_request.base.sha  (pull_request only)
#   BEFORE    github.event.before                 (push only)
# Outputs (to $GITHUB_OUTPUT, or stdout when run locally):
#   matrix    JSON array of service paths for the build matrix
#   any       "true" if anything needs building at all
set -euo pipefail

ALL=(
  services/api-gateway
  services/ingestion
  services/decision-engine
  services/classifier
  services/executor
  services/audit
  services/reporting
  demo/world-simulator
  demo/notification-simulator
)

# Shared inputs that every image embeds. Each Dockerfile builds with the repo
# root as its context and COPYs the whole tree, so these are not per-service
# (docs/ARCHITECTURE.md 2a). The workflow itself is included because a change
# to how images are built must be proven against all of them.
SHARED_RE='^(go\.mod|go\.sum|internal/platform/|proto/|\.github/)'

emit() {
  local matrix="$1" any="$2"
  if [ -n "${GITHUB_OUTPUT:-}" ]; then
    printf 'matrix=%s\n' "$matrix" >>"$GITHUB_OUTPUT"
    printf 'any=%s\n' "$any" >>"$GITHUB_OUTPUT"
  else
    printf 'matrix=%s\nany=%s\n' "$matrix" "$any"
  fi
}

# Built by hand rather than with jq so this script can be run and tested
# locally, not only on a runner that happens to ship jq. Safe because every
# value is one of the fixed ALL entries, never arbitrary input.
as_json() {
  local out="" item
  for item in "$@"; do
    [ -n "$out" ] && out="${out},"
    out="${out}\"${item}\""
  done
  printf '[%s]' "$out"
}

all_json=$(as_json "${ALL[@]}")

case "${EVENT:-}" in
  pull_request) base="${BASE_SHA:-}" ;;
  push) base="${BEFORE:-}" ;;
  *) base="" ;;
esac

# A new branch or a force push reports an all-zero "before"; a shallow clone
# may not have the base commit at all. Either way we cannot diff, so build all.
if [ -z "$base" ] ||
  [ "$base" = "0000000000000000000000000000000000000000" ] ||
  ! git cat-file -e "${base}^{commit}" 2>/dev/null; then
  echo "diff base unavailable (event=${EVENT:-none}, base=${base:-none}); building every image" >&2
  emit "$all_json" true
  exit 0
fi

changed=$(git diff --name-only "$base" HEAD)
if [ -z "$changed" ]; then
  echo "no files changed; building nothing" >&2
  emit '[]' false
  exit 0
fi

# Markdown never affects a compiled binary or an image, so a README or SPEC
# under a service directory should not trigger its rebuild.
code_changed=$(printf '%s\n' "$changed" | grep -vE '\.md$' || true)
if [ -z "$code_changed" ]; then
  echo "only markdown changed; building nothing" >&2
  emit '[]' false
  exit 0
fi

if printf '%s\n' "$code_changed" | grep -qE "$SHARED_RE"; then
  echo "shared code changed; building every image" >&2
  emit "$all_json" true
  exit 0
fi

selected=()
for svc in "${ALL[@]}"; do
  if printf '%s\n' "$code_changed" | grep -q "^${svc}/"; then
    selected+=("$svc")
  fi
done

if [ ${#selected[@]} -eq 0 ]; then
  echo "no service directory changed; building nothing" >&2
  emit '[]' false
  exit 0
fi

echo "building: ${selected[*]}" >&2
emit "$(as_json "${selected[@]}")" true

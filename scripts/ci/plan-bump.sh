#!/usr/bin/env bash
# The planning step of .github/workflows/bump-consumers.yml: which go-common
# commit is pinned, and in which consumers. Writes `sha`, `dry_run`, `count` and
# `matrix` to $GITHUB_OUTPUT. A script so scripts/ci/bump_consumer_test.sh can
# run it with a fake `gh`.
#
# Environment
#   EVENT          workflow_run | workflow_dispatch
#   RUN_SHA        workflow_run: the head_sha of the Go CI run that succeeded (the
#                  workflow's job guard has already checked success / push / main)
#   IGNORED_PATHS  workflow_run: newline-separated globs (`*` and `**` match across
#                  `/`). A commit whose changed files ALL match starts no bump.
#                  Empty = nothing is ignored. Not applied to workflow_dispatch.
#   GOCOMMON_DIR   a git checkout of go-common to read the commit's files from
#                  (default `.`; the commit and its parent are fetched if missing)
#   INPUT_SHA      dispatch input: the commit to pin; empty = the head of main
#   INPUT_ONLY     dispatch input: one repo (short name) instead of the list
#   INPUT_DRY_RUN  dispatch input; anything but 'false' is a dry run
#   BUMP_TOKEN     the GO_COMMON_BUMP_TOKEN secret (may be empty)
#   GOCOMMON_REPO  default STECH-Super-App/go-common
#   REPO_OWNER     default STECH-Super-App
#   CONSUMERS_FILE default .github/consumers.json
#   GH_TOKEN       a token that can read go-common (the run's own is enough: it is public)
set -euo pipefail

gocommon_repo=${GOCOMMON_REPO:-STECH-Super-App/go-common}
consumers_file=${CONSUMERS_FILE:-.github/consumers.json}
out=${GITHUB_OUTPUT:-/dev/stdout}

notice() { echo "::notice::$*"; }
warn() { echo "::warning::$*"; }
fail() {
  echo "::error::$*"
  exit 1
}
emit() { # emit <sha> <dry_run> <matrix json>
  local count
  count=$(jq -r '.include | length' <<<"$3")
  {
    echo "sha=$1"
    echo "dry_run=$2"
    echo "count=${count}"
    echo "matrix=$3"
  } >>"$out"
}
empty='{"include":[]}'

# Without the secret the workflow does nothing and ends green, so it can be
# merged before the token exists.
if [ -z "${BUMP_TOKEN:-}" ]; then
  notice "GO_COMMON_BUMP_TOKEN is not set: no consumer is bumped. Create the secret (README, 'How a go-common change reaches the services') to switch this workflow on."
  emit "" true "$empty"
  exit 0
fi

# Consumers only ever pin a commit on main (Critical Rule 9 — Push policy split:
# a pin to a branch commit must never reach a consumer's main).
require_on_main() { # require_on_main <sha>
  local relation
  relation=$(gh api "repos/${gocommon_repo}/compare/$1...main" --jq .status)
  case "$relation" in
    ahead | identical) ;;
    *) fail "${1:0:7} is not on go-common's main (compare says '${relation}'): refusing to pin it" ;;
  esac
}

# only_ignored_paths <sha>: 0 when the commit changed files and EVERY one matches
# IGNORED_PATHS. Anything else, including a commit whose files cannot be read (a
# root commit, a parent that cannot be fetched), is 1: fail closed, the bump goes
# ahead. The diff is against the commit's first parent, i.e. the whole change a
# squash or merge commit brought in.
only_ignored_paths() {
  local sha=$1 dir=${GOCOMMON_DIR:-.} line file pattern ignored files=()
  local patterns=()
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    case "$line" in '#'*) continue ;; esac
    patterns+=("$line")
  done <<<"${IGNORED_PATHS:-}"
  [ "${#patterns[@]}" -gt 0 ] || return 1

  have_parent() { git -C "$dir" rev-parse --verify --quiet "${sha}^1^{commit}" >/dev/null 2>&1; }
  have_parent || git -C "$dir" fetch --quiet --no-tags --depth=2 origin "$sha" >/dev/null 2>&1 || true
  have_parent || {
    warn "cannot read the files of ${sha:0:7} (no parent commit available): not skipping the bump"
    return 1
  }
  while IFS= read -r file; do
    [ -n "$file" ] && files+=("$file")
  done < <(git -C "$dir" diff --name-only "${sha}^1" "$sha" 2>/dev/null)
  [ "${#files[@]}" -gt 0 ] || return 1

  for file in "${files[@]}"; do
    ignored=0
    for pattern in "${patterns[@]}"; do
      # The pattern is a glob on purpose: case matches `*` across `/`.
      # shellcheck disable=SC2254
      case "$file" in
        $pattern)
          ignored=1
          break
          ;;
      esac
    done
    [ "$ignored" -eq 1 ] || return 1
  done
  return 0
}

dry_run=false
case "${EVENT:-}" in
  workflow_run)
    sha=${RUN_SHA:-}
    [[ $sha =~ ^[0-9a-f]{40}$ ]] || fail "the Go CI run's head_sha is not a full lowercase 40-hex sha (got '${sha}')"
    require_on_main "$sha"
    if only_ignored_paths "$sha"; then
      notice "${sha:0:7} changed only docs, the workflow or its scripts (IGNORED_PATHS): no consumer is bumped"
      emit "$sha" false "$empty"
      exit 0
    fi
    ;;
  workflow_dispatch)
    [ "${INPUT_DRY_RUN:-true}" = "false" ] || dry_run=true
    sha=${INPUT_SHA:-}
    if [ -z "$sha" ]; then
      sha=$(gh api "repos/${gocommon_repo}/commits/main" --jq .sha)
    fi
    [[ $sha =~ ^[0-9a-f]{7,40}$ ]] || fail "sha must be 7 to 40 lowercase hex characters, got '${sha}'"
    sha=$(gh api "repos/${gocommon_repo}/commits/${sha}" --jq .sha)
    require_on_main "$sha"
    ;;
  *)
    fail "unsupported event '${EVENT:-}'"
    ;;
esac
[[ ${sha:-} =~ ^[0-9a-f]{40}$ ]] || fail "no usable go-common commit (got '${sha:-}')"

only=${INPUT_ONLY:-}
if [ -n "$only" ]; then
  jq -e --arg only "$only" '.consumers | map(.repo) | index($only)' "$consumers_file" >/dev/null ||
    fail "'${only}' is not in ${consumers_file}"
  if jq -e --arg only "$only" '.consumers[] | select(.repo == $only) | .enabled == false' "$consumers_file" >/dev/null; then
    notice "${only} is switched off in ${consumers_file} (enabled: false): nothing to do"
    emit "$sha" "$dry_run" "$empty"
    exit 0
  fi
fi

matrix=$(jq -c --arg only "$only" '{
  include: [
    .consumers[]
    | select(.enabled != false)
    | select($only == "" or .repo == $only)
    | {
        repo,
        module_dir,
        auto_merge: ((if has("auto_merge") then .auto_merge else true end) | tostring),
        required_checks: ((.required_checks // []) | tojson),
        # absent = deploy.yml; "" = no deploy workflow, nothing to wait for
        deploy_workflow: (if (.deploy_workflow | type) == "string" then .deploy_workflow else "deploy.yml" end)
      }
  ]
}' "$consumers_file")

echo "go-common ${sha:0:7}, dry run: ${dry_run}, consumers: $(jq -r '[.include[].repo] | join(", ")' <<<"$matrix")"
emit "$sha" "$dry_run" "$matrix"

#!/usr/bin/env bash
# The planning step of .github/workflows/bump-consumers.yml: which go-common
# commit is pinned, and in which consumers. Writes `sha`, `dry_run`, `count` and
# `matrix` to $GITHUB_OUTPUT. A script so scripts/ci/bump_consumer_test.sh can
# run it with a fake `gh`.
#
# Environment
#   EVENT          push | workflow_dispatch
#   PUSH_SHA       the pushed commit (a push run)
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

dry_run=false
case "${EVENT:-}" in
  push)
    sha=${PUSH_SHA:-}
    ;;
  workflow_dispatch)
    [ "${INPUT_DRY_RUN:-true}" = "false" ] || dry_run=true
    sha=${INPUT_SHA:-}
    if [ -z "$sha" ]; then
      sha=$(gh api "repos/${gocommon_repo}/commits/main" --jq .sha)
    fi
    [[ $sha =~ ^[0-9a-f]{7,40}$ ]] || fail "sha must be 7 to 40 lowercase hex characters, got '${sha}'"
    sha=$(gh api "repos/${gocommon_repo}/commits/${sha}" --jq .sha)
    # Consumers only ever pin a commit on main (Critical Rule 9 — Push policy
    # split: a pin to a branch commit must never reach a consumer's main).
    relation=$(gh api "repos/${gocommon_repo}/compare/${sha}...main" --jq .status)
    case "$relation" in
      ahead | identical) ;;
      *) fail "${sha:0:7} is not on go-common's main (compare says '${relation}'): refusing to pin it" ;;
    esac
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
        deploy_workflow: (.deploy_workflow // "deploy.yml")
      }
  ]
}' "$consumers_file")

echo "go-common ${sha:0:7}, dry run: ${dry_run}, consumers: $(jq -r '[.include[].repo] | join(", ")' <<<"$matrix")"
emit "$sha" "$dry_run" "$matrix"

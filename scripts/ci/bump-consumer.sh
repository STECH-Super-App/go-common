#!/usr/bin/env bash
# Bump go-common in ONE consumer repo: pin it to GOCOMMON_SHA, open a pull
# request, switch on GitHub auto-merge where that is safe, and (when auto-merge
# is on) wait until the merge has deployed. Called once per matrix job by
# .github/workflows/bump-consumers.yml; written as a script so
# scripts/ci/bump_consumer_test.sh can run it against a fake `gh` and a local
# bare repository.
#
# The consumer's `main` is already checked out in CONSUMER_DIR with a remote
# `origin` that accepts the push (actions/checkout with the bump token).
#
# Environment
#   GO_COMMON_BUMP_TOKEN  org token that may push branches and open PRs in the
#                         consumer. ABSENT => a notice and exit 0: the workflow
#                         may be merged before the secret exists.
#   CONSUMER_REPO         owner/name of the consumer, e.g. STECH-Super-App/auth-service
#   CONSUMER_DIR          the checkout of its main
#   MODULE_DIR            directory of the go.mod inside it ('.' or services/outbox-worker)
#   GOCOMMON_SHA          full sha of the go-common commit to pin (on go-common's main)
#   GOCOMMON_DIR          a full-history checkout of go-common (commit subjects, the
#                         auth-path check)
#   GOCOMMON_REPO         default STECH-Super-App/go-common
#   AUTO_MERGE            'false' = never switch auto-merge on here (consumers.json)
#   REQUIRED_CHECKS       JSON array of check names that must be required on main
#   DEPLOY_WORKFLOW       workflow file whose run on the merge commit is awaited (deploy.yml)
#   DRY_RUN               'true' = compute everything, change nothing anywhere
#   PUSH_BEFORE           the push's `before` sha: the push range is PUSH_BEFORE..GOCOMMON_SHA
#   AUTH_PATHS, AUTH_DEPS see auth-paths.sh
#   GEN_GO_PAT            read access to the private modules, as in go-common's ci.yml
#                         (falls back to the bump token)
#   WAIT_SECONDS (1800)   one budget for "merged and deployed"; POLL_SECONDS (20);
#                         DEPLOY_APPEAR_SECONDS (300); ENABLE_RETRIES (5), ENABLE_RETRY_SECONDS (10)
#   BUMP_ACTOR_LOGIN     login the bump PRs are authored by. Default: `gh api user`, i.e. the
#                         token's own user. Set it when the token is an App installation
#                         token (which cannot read /user); without any identity no earlier
#                         PR is trusted and auto-merge is never armed on a reused one.
#   GITHUB_STEP_SUMMARY, GITHUB_SERVER_URL, GITHUB_REPOSITORY, GITHUB_RUN_ID: optional
#
# Auto-merge is armed only on a head commit this script created (or, for a PR of an
# earlier run, one whose go.mod/go.sum are byte-identical to what it just computed),
# with that commit's sha as expectedHeadOid: a later push to the branch disarms it.
# A PR on the bump branch name that is not the token user's own same-repository PR is
# never reused, closed, pushed over or armed; the job stops with an error instead.
#
# Exit: 0 done (PR opened, nothing to do, or no token); 1 something needs a human.
set -euo pipefail

GOCOMMON_MODULE=github.com/STECH-Super-App/go-common
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

notice() { echo "::notice::$*"; }
warn() { echo "::warning::$*"; }
fail() {
  echo "::error::$*"
  exit 1
}
summary() {
  if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
    echo "$*" >>"$GITHUB_STEP_SUMMARY"
  fi
}

token=${GO_COMMON_BUMP_TOKEN:-}
if [ -z "$token" ]; then
  notice "GO_COMMON_BUMP_TOKEN is not set: nothing is bumped. Create the secret (see README, 'How a go-common change reaches the services') to switch this workflow on."
  summary "- ${CONSUMER_REPO:-?}: skipped, GO_COMMON_BUMP_TOKEN is not set"
  exit 0
fi
export GH_TOKEN=$token

: "${CONSUMER_REPO:?CONSUMER_REPO is required}"
: "${CONSUMER_DIR:?CONSUMER_DIR is required}"
: "${GOCOMMON_SHA:?GOCOMMON_SHA is required}"
: "${GOCOMMON_DIR:?GOCOMMON_DIR is required}"
module_dir=${MODULE_DIR:-.}
gocommon_repo=${GOCOMMON_REPO:-STECH-Super-App/go-common}
auto_merge_flag=${AUTO_MERGE:-true}
required_checks=${REQUIRED_CHECKS:-[]}
deploy_workflow=${DEPLOY_WORKFLOW:-deploy.yml}
dry_run=${DRY_RUN:-false}
wait_seconds=${WAIT_SECONDS:-1800}
poll_seconds=${POLL_SECONDS:-20}
deploy_appear_seconds=${DEPLOY_APPEAR_SECONDS:-300}
enable_retries=${ENABLE_RETRIES:-5}
enable_retry_seconds=${ENABLE_RETRY_SECONDS:-10}

if ! [[ $GOCOMMON_SHA =~ ^[0-9a-f]{40}$ ]]; then
  fail "GOCOMMON_SHA must be a full 40-character sha, got '${GOCOMMON_SHA}'"
fi
sha7=${GOCOMMON_SHA:0:7}
branch="chore/go-common-${sha7}"

# Path of the module relative to the repo root, without './' or a trailing '/'.
rel=${module_dir#./}
rel=${rel%/}
[ "$rel" = "." ] && rel=""
mod_path=$CONSUMER_DIR${rel:+/$rel}
mod_prefix=${rel:+$rel/}
allowed_files=("${mod_prefix}go.mod" "${mod_prefix}go.sum")

[ -f "$mod_path/go.mod" ] || fail "no go.mod in ${mod_path}"

git_c() { git -C "$CONSUMER_DIR" "$@"; }

# go.mod's requirement of go-common (works for a single-line and a block require).
pinned_version() {
  awk -v m="$GOCOMMON_MODULE" '
    $1 == m { v = $2 }
    $1 == "require" && $2 == m { v = $3 }
    END { print v }
  ' "$mod_path/go.mod"
}

echo "== ${CONSUMER_REPO} (${module_dir}) -> go-common ${sha7}"

# --- Where is the consumer now? --------------------------------------------
# Map the previous pin to a go-common commit: every pseudo-version form
# (vX.Y.Z-<ts>-<sha>, vX.Y.Z-0.<ts>-<sha>, vX.Y.Z-pre.0.<ts>-<sha>, +incompatible)
# carries a 12-hex commit prefix; a plain tag is resolved through git in the
# go-common checkout. prev_sha is the FULL sha, or empty with prev_problem saying
# why; an empty prev_sha blocks auto-merge below (the range since the previous pin
# cannot be checked for auth changes) but never the PR itself.
prev_version=$(pinned_version)
prev_sha=""
prev_problem=""
pseudo_re='^v[0-9]+\.[0-9]+\.[0-9]+-([0-9A-Za-z.-]*[-.])?[0-9]{14}-([0-9a-f]{12})(\+incompatible)?$'
tag_re='^(v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?)(\+incompatible)?$'
if [ -z "$prev_version" ]; then
  prev_problem="the consumer has no previous go-common requirement"
else
  prev_ref=""
  if [[ $prev_version =~ $pseudo_re ]]; then
    prev_ref=${BASH_REMATCH[2]}
  elif [[ $prev_version =~ $tag_re ]]; then
    prev_ref=refs/tags/${BASH_REMATCH[1]}
  fi
  if [ -n "$prev_ref" ]; then
    prev_sha=$(git -C "$GOCOMMON_DIR" rev-parse --verify -q "${prev_ref}^{commit}" 2>/dev/null) || prev_sha=""
  fi
  if [ -z "$prev_sha" ]; then
    prev_problem="previous pin ${prev_version} cannot be mapped to a commit"
  fi
fi
if [ -n "$prev_sha" ] && [ "$GOCOMMON_SHA" = "$prev_sha" ]; then
  notice "${CONSUMER_REPO} is already on go-common ${sha7}: no pull request"
  summary "- ${CONSUMER_REPO}: already on ${sha7}"
  exit 0
fi

cmp_json=""
cmp_status=""
if [ -n "$prev_sha" ]; then
  if cmp_json=$(gh api "repos/${gocommon_repo}/compare/${prev_sha}...${GOCOMMON_SHA}" 2>/dev/null); then
    cmp_status=$(jq -r '.status // ""' <<<"$cmp_json")
  else
    warn "could not compare go-common ${prev_sha:0:7}...${sha7}: the PR body will not list the commits"
    cmp_json=""
  fi
fi
case "$cmp_status" in
  behind | identical)
    notice "${CONSUMER_REPO} already holds go-common ${prev_sha:0:7}, which is ${cmp_status} ${sha7}: no pull request"
    summary "- ${CONSUMER_REPO}: pin ${prev_sha:0:7} is not older than ${sha7}"
    exit 0
    ;;
esac

# --- Do the bump ------------------------------------------------------------
# Private modules: the same trick as the consumers' CI, but handed to the go
# commands through the environment instead of `git config --global`. A global
# insteadOf would also rewrite the URL of the `git push` below and push with the
# read-only module PAT instead of the bump token.
go_pat=${GEN_GO_PAT:-$token}
(
  cd "$mod_path"
  export GOPRIVATE="${GOPRIVATE:-github.com/STECH-Super-App/*}"
  export GIT_TERMINAL_PROMPT=0
  export GIT_CONFIG_COUNT=1
  export GIT_CONFIG_KEY_0="url.https://${go_pat}:x-oauth-basic@github.com/.insteadOf"
  export GIT_CONFIG_VALUE_0="https://github.com/"
  go get "${GOCOMMON_MODULE}@${GOCOMMON_SHA}"
  go mod tidy
)

mapfile -t changed < <(git_c status --porcelain --untracked-files=all | sed -e 's/^...//')
if [ "${#changed[@]}" -eq 0 ]; then
  notice "${CONSUMER_REPO}: go get changed nothing (already on ${sha7}): no pull request"
  summary "- ${CONSUMER_REPO}: nothing to change"
  exit 0
fi
stray=()
for path in "${changed[@]}"; do
  ok=0
  for allowed in "${allowed_files[@]}"; do
    [ "$path" = "$allowed" ] && ok=1
  done
  [ "$ok" -eq 1 ] || stray+=("$path")
done
if [ "${#stray[@]}" -gt 0 ]; then
  summary "- ${CONSUMER_REPO}: REFUSED, the bump changes more than go.mod/go.sum: ${stray[*]}"
  fail "${CONSUMER_REPO}: the bump changes files besides ${allowed_files[*]}: ${stray[*]} — not a pure bump, nothing pushed"
fi
new_version=$(pinned_version)
# What the bump is computed against: the checkout of main, before any branch.
base_sha=$(git_c rev-parse HEAD)

# --- Title, body, auto-merge decision ---------------------------------------
subject=$(git -C "$GOCOMMON_DIR" log -1 --format=%s "$GOCOMMON_SHA" 2>/dev/null || true)
[ -n "$subject" ] || subject=$(gh api "repos/${gocommon_repo}/commits/${GOCOMMON_SHA}" --jq '.commit.message | split("\n")[0]' 2>/dev/null || true)
[ -n "$subject" ] || subject="go-common ${sha7}"
title="chore: go-common ${sha7} — ${subject}"

# Reasons auto-merge stays off; empty = eligible.
reasons=()

if [ "$auto_merge_flag" = "false" ]; then
  reasons+=("auto_merge is false for this repository in .github/consumers.json")
fi

# Critical Rule 20 (Token size budget): a change to the token or the auth
# middleware needs the owner's sign-off and a real login on dev. Two ranges: the
# commits of THIS push (the owner's rule) and every commit since the consumer's
# previous pin — a push that auto-merges must not carry an earlier auth change
# along that was left unmerged without auto-merge.
auth_lines=""
check_auth_range() {
  local from=$1 label=$2 out rc=0
  out=$(bash "$here/auth-paths.sh" "$GOCOMMON_DIR" "$from" "$GOCOMMON_SHA") || rc=$?
  case "$rc" in
    0) ;;
    10) auth_lines+="[${label}] ${out//$'\n'/; } " ;;
    *) auth_lines+="[${label}] the range could not be checked (${out//$'\n'/; }) " ;;
  esac
}
push_before=${PUSH_BEFORE:-}
if [ -n "$push_before" ] && [[ ! $push_before =~ ^0+$ ]]; then
  check_auth_range "$push_before" "this push"
fi
if [ -n "$prev_sha" ]; then
  check_auth_range "$prev_sha" "since the previous pin"
else
  # Fail closed: an unmapped pin leaves the range since it unchecked, and a push
  # after an unmerged auth bump could carry that change in under its own auto-merge.
  reasons+=("${prev_problem} (the go-common commits since it could not be checked for token / auth-middleware changes, Critical Rule 20 — Token size budget)")
fi
if [ -n "$auth_lines" ]; then
  reasons+=("the go-common commits touch the token / auth-middleware code, which needs the owner's sign-off and a real login on dev (Critical Rule 20 — Token size budget): ${auth_lines% }")
fi

if [ "${#reasons[@]}" -eq 0 ]; then
  repo_json=$(gh api "repos/${CONSUMER_REPO}" 2>/dev/null) || repo_json='{}'
  allow_auto=$(jq -r '.allow_auto_merge // false' <<<"$repo_json")
  allow_squash=$(jq -r '.allow_squash_merge // false' <<<"$repo_json")
  if [ "$allow_auto" != "true" ]; then
    reasons+=("auto-merge is not allowed on the repository (Settings > General > Allow auto-merge) or the token cannot see the setting")
  fi
  if [ "$allow_squash" != "true" ]; then
    reasons+=("squash merging is not allowed on the repository")
  fi

  # Required status checks on main: classic branch protection and rulesets.
  required=""
  prot_err=""
  if prot=$(gh api "repos/${CONSUMER_REPO}/branches/main/protection/required_status_checks" \
    --jq '[(.contexts // [])[], ((.checks // [])[].context)] | unique | .[]' 2>&1); then
    required+="${prot}"$'\n'
  elif ! grep -q 'HTTP 404' <<<"$prot"; then
    prot_err="could not read the branch protection of main (the token needs Administration: read): ${prot%%$'\n'*}"
  fi
  if rules=$(gh api "repos/${CONSUMER_REPO}/rules/branches/main" \
    --jq '[.[] | select(.type == "required_status_checks") | (.parameters.required_status_checks // [])[].context] | unique | .[]' 2>&1); then
    required+="${rules}"$'\n'
  else
    warn "${CONSUMER_REPO}: could not read the rulesets of main: ${rules%%$'\n'*}"
  fi
  required=$(grep -v '^$' <<<"$required" | sort -u || true)
  if [ -z "$required" ]; then
    reasons+=("${prot_err:-branch main has no required status checks}")
  else
    missing=()
    while IFS= read -r want; do
      [ -z "$want" ] && continue
      grep -qxF -- "$want" <<<"$required" || missing+=("$want")
    done < <(jq -r '.[]' <<<"$required_checks")
    if [ "${#missing[@]}" -gt 0 ]; then
      reasons+=("these checks are not required on main: $(printf '%s, ' "${missing[@]}" | sed 's/, $//')")
    fi
  fi
fi

if [ "${#reasons[@]}" -eq 0 ]; then
  am_line="enabled (squash, merges when the required checks pass)"
else
  am_line="not enabled — $(printf '%s; ' "${reasons[@]}" | sed 's/; $//')"
fi

run_url=""
if [ -n "${GITHUB_RUN_ID:-}" ] && [ -n "${GITHUB_REPOSITORY:-}" ]; then
  run_url="${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}"
fi

body_file=$(mktemp)
trap 'rm -f "$body_file"' EXIT
{
  echo "Bumps go-common to [\`${sha7}\`](https://github.com/${gocommon_repo}/commit/${GOCOMMON_SHA}): ${subject}"
  echo
  echo "- Previous pin: \`${prev_version:-none}\`"
  echo "- New pin: \`${new_version}\`"
  echo "- Files: \`${allowed_files[0]}\` and \`${allowed_files[1]}\` only"
  echo "- Auto-merge: ${am_line}"
  echo
  if [ -n "$cmp_json" ]; then
    total=$(jq -r '.total_commits // (.commits | length)' <<<"$cmp_json")
    echo "go-common commits since the previous pin (${total}):"
    echo
    jq -r '.commits[] | "- [`" + .sha[0:7] + "`](" + .html_url + ") " + (.commit.message | split("\n")[0])' <<<"$cmp_json" | head -n 50
    if [ "$total" -gt 50 ]; then
      echo "- … and $((total - 50)) more"
    fi
  else
    echo "go-common commits since the previous pin: not available (the previous pin cannot be mapped to a go-common commit, or the compare call failed)."
  fi
  echo
  echo "**\`go build ./...\` was not run by go-common's workflow.** This PR's own checks are the test: a red check means the bump does not fit this service yet."
  if [ -n "$run_url" ]; then
    echo
    echo "Opened by go-common's \`Bump consumers\` workflow: ${run_url}"
  fi
} >"$body_file"

# --- Open bump PRs -----------------------------------------------------------
# Never two open bump PRs in one repo. Same branch = this very bump, opened by an
# earlier run of the same push: reuse it. Another branch = an older go-common
# commit: close it (that also stops its auto-merge) and delete its branch.
#
# Only PRs this job could have opened are touched: same repository (not a fork)
# and authored by the bump token's own user. Anyone with push access can name a
# branch chore/go-common-<anything>, and anyone can open a fork PR with that head
# name; such a PR is never reused, closed or pushed over, and never armed.
owner=${CONSUMER_REPO%%/*}
actor=${BUMP_ACTOR_LOGIN:-}
if [ -z "$actor" ]; then
  actor=$(gh api user --jq .login 2>/dev/null) || actor=""
fi
if [ -z "$actor" ]; then
  warn "${CONSUMER_REPO}: cannot read the bump token's user (gh api user); no earlier bump PR is trusted"
fi

existing_url=""
superseded=()
blocked=()
open_bumps=$(gh pr list -R "$CONSUMER_REPO" --state open --base main --limit 100 \
  --json number,headRefName,url,isCrossRepository,headRepositoryOwner,author |
  jq -r --arg owner "$owner" --arg actor "$actor" '
    .[] | select(.headRefName | startswith("chore/go-common-"))
    | (if .isCrossRepository != false then "it comes from a fork or another repository"
       elif ((.headRepositoryOwner.login // "") | ascii_downcase) != ($owner | ascii_downcase) then "its head repository is not owned by " + $owner
       elif $actor == "" then "the bump token user is unknown"
       elif ((.author.login // "") | ascii_downcase) != ($actor | ascii_downcase) then "authored by " + (.author.login // "an unknown user") + ", not by " + $actor
       else "" end) as $why
    | [(if $why == "" then "1" else "0" end), (.number | tostring), .headRefName, .url, $why] | @tsv')
while IFS=$'\t' read -r trusted number head url why; do
  [ -z "$number" ] && continue
  if [ "$trusted" = "1" ]; then
    if [ "$head" = "$branch" ]; then
      existing_url=$url
    else
      superseded+=("${number}"$'\t'"${head}")
    fi
  elif [ "$head" = "$branch" ]; then
    blocked+=("${url} (${why})")
  else
    warn "${CONSUMER_REPO}: leaving ${url} alone: not a bump PR of this job (${why})"
  fi
done <<<"$open_bumps"

# A foreign PR on OUR branch name: pushing would overwrite its branch (or sit next
# to a look-alike), so nothing is pushed, opened or armed here. The job fails so a
# human looks: it is either a mistake or someone steering the auto-merge.
blocked_text=""
if [ "${#blocked[@]}" -gt 0 ]; then
  blocked_text="an open PR uses the bump branch name ${branch} and is not this job's own same-repository PR: $(printf '%s; ' "${blocked[@]}" | sed 's/; $//')"
fi

if [ "$dry_run" = "true" ]; then
  echo "DRY RUN: nothing is pushed, opened or closed."
  git_c diff --stat
  if [ -n "$blocked_text" ]; then
    echo "WOULD refuse: ${blocked_text}"
  fi
  for item in "${superseded[@]}"; do
    echo "WOULD close superseded bump PR #${item%%$'\t'*} and delete branch ${item#*$'\t'}"
  done
  if [ -n "$existing_url" ]; then
    echo "WOULD reuse the open PR ${existing_url} if its head matches this bump byte for byte"
  else
    echo "WOULD push branch ${branch} and open a PR"
  fi
  echo "WOULD use title: ${title}"
  echo "WOULD use body:"
  sed 's/^/    /' "$body_file"
  echo "WOULD handle auto-merge: ${am_line}"
  summary "- ${CONSUMER_REPO}: dry run, would open ${branch}; auto-merge: ${am_line}"
  exit 0
fi

if [ -n "$blocked_text" ]; then
  summary "- ${CONSUMER_REPO}: REFUSED, ${blocked_text}"
  fail "${CONSUMER_REPO}: ${blocked_text} — nothing pushed, opened or armed, and that PR is left untouched"
fi

for item in "${superseded[@]}"; do
  number=${item%%$'\t'*}
  old_branch=${item#*$'\t'}
  echo "closing superseded bump PR #${number} (${old_branch})"
  gh pr close "$number" -R "$CONSUMER_REPO" \
    --comment "Superseded by the bump to go-common ${sha7}${run_url:+ (${run_url})}. This branch is deleted; the newer pull request replaces it."
  gh api -X DELETE "repos/${CONSUMER_REPO}/git/refs/heads/${old_branch}" >/dev/null ||
    warn "could not delete branch ${old_branch} in ${CONSUMER_REPO}"
done

pushed_sha=""
if [ -n "$existing_url" ]; then
  pr_url=$existing_url
  echo "the bump PR for ${sha7} is already open: ${pr_url}"
else
  git_c switch -q -c "$branch"
  git_c add -- "${allowed_files[@]}"
  git_c -c user.name="github-actions[bot]" -c user.email="41898282+github-actions[bot]@users.noreply.github.com" \
    commit -q -m "$title"
  pushed_sha=$(git_c rev-parse HEAD)
  # A leftover branch of the same name (a closed PR of an earlier attempt) holds
  # the same bump; replacing it is safe.
  git_c push -q --force origin "$branch"
  pr_url=$(gh pr create -R "$CONSUMER_REPO" --base main --head "$branch" \
    --title "$title" --body-file "$body_file" | tail -n 1)
  echo "opened ${pr_url}"
fi
summary "- ${CONSUMER_REPO}: ${pr_url} — auto-merge: ${am_line}"

if [ "${#reasons[@]}" -gt 0 ]; then
  notice "${CONSUMER_REPO}: PR opened without auto-merge: ${am_line}"
  exit 0
fi

# --- Switch auto-merge on ----------------------------------------------------
# Last look, from the pull request's actual head commit and not from a file list:
# on top of the main this bump was computed against, the commit changes go.mod and
# go.sum of the module and nothing else, both byte-identical to the files computed
# here, and the go.mod has no replace directive. Sets verify_error on failure.
verify_error=""
verify_head() {
  local oid=$1 fetched names path allowed ok want have
  if ! [[ $oid =~ ^[0-9a-f]{40}$ ]]; then
    verify_error="the head commit of the PR is unreadable (${oid:-empty})"
    return 1
  fi
  if ! git_c fetch -q origin "refs/heads/${branch}" 2>/dev/null; then
    verify_error="branch ${branch} cannot be fetched from ${CONSUMER_REPO}"
    return 1
  fi
  fetched=$(git_c rev-parse --verify -q "FETCH_HEAD^{commit}") || fetched=""
  if [ "$fetched" != "$oid" ]; then
    verify_error="the head of the PR (${oid:0:7}) is not the tip of branch ${branch} (${fetched:0:7}): it moved"
    return 1
  fi
  mapfile -t names < <(git_c diff --name-only "$base_sha" "$oid")
  for path in "${names[@]}"; do
    ok=0
    for allowed in "${allowed_files[@]}"; do
      [ "$path" = "$allowed" ] && ok=1
    done
    if [ "$ok" -ne 1 ]; then
      verify_error="the head commit ${oid:0:7} changes ${path} against main, not only go.mod and go.sum"
      return 1
    fi
  done
  for path in "${allowed_files[@]}"; do
    want=absent
    [ -f "$CONSUMER_DIR/$path" ] && want=$(git_c hash-object --no-filters -- "$path")
    have=$(git_c rev-parse --verify -q "${oid}:${path}") || have=absent
    if [ "$want" != "$have" ]; then
      verify_error="${path} in the head commit ${oid:0:7} is not what this job computed (${have:0:7} vs ${want:0:7})"
      return 1
    fi
  done
  if git_c show "${oid}:${mod_prefix}go.mod" | grep -Eq '^[[:space:]]*replace([[:space:]]|\()'; then
    verify_error="${mod_prefix}go.mod in the head commit ${oid:0:7} has a replace directive"
    return 1
  fi
  return 0
}

mapfile -t pr_files < <(gh pr view "$pr_url" --json files --jq '.files[].path')
for path in "${pr_files[@]}"; do
  ok=0
  for allowed in "${allowed_files[@]}"; do
    [ "$path" = "$allowed" ] && ok=1
  done
  [ "$ok" -eq 1 ] || fail "${CONSUMER_REPO}: ${pr_url} changes ${path}, not a pure bump: auto-merge NOT enabled"
done

IFS=$'\t' read -r node_id head_oid < <(gh pr view "$pr_url" --json id,headRefOid --jq '[.id, .headRefOid] | @tsv')
if [ -n "$pushed_sha" ] && [ "$head_oid" != "$pushed_sha" ]; then
  summary "- ${CONSUMER_REPO}: ${pr_url} head is not the commit pushed here: auto-merge NOT enabled"
  fail "${CONSUMER_REPO}: ${pr_url} has head ${head_oid:0:7}, but this job pushed ${pushed_sha:0:7}: auto-merge NOT enabled"
fi
if ! verify_head "$head_oid"; then
  summary "- ${CONSUMER_REPO}: ${pr_url} auto-merge NOT enabled: ${verify_error}"
  fail "${CONSUMER_REPO}: ${pr_url} is not reusable, auto-merge NOT enabled: ${verify_error}"
fi

# The GraphQL mutation, not `gh pr merge --auto`: it only ever arms auto-merge
# and errors on a pull request that is already mergeable, where the CLI could
# merge it straight away. expectedHeadOid = the commit verified above: a push to
# the branch after this point makes GitHub refuse (or cancel) the merge. Right
# after creation GitHub may still be registering the required checks, hence the
# retries.
enabled=0
for ((attempt = 1; attempt <= enable_retries; attempt++)); do
  # shellcheck disable=SC2016 # $id and $oid are GraphQL variables, not shell ones
  if gh api graphql \
    -f query='mutation($id: ID!, $oid: GitObjectID!) { enablePullRequestAutoMerge(input: {pullRequestId: $id, mergeMethod: SQUASH, expectedHeadOid: $oid}) { pullRequest { number } } }' \
    -f id="$node_id" -f oid="$head_oid" >/dev/null; then
    enabled=1
    break
  fi
  echo "auto-merge not accepted yet (attempt ${attempt}/${enable_retries})"
  sleep "$enable_retry_seconds"
done
if [ "$enabled" -ne 1 ]; then
  warn "${CONSUMER_REPO}: could not enable auto-merge on ${pr_url}; the PR stays open for a human"
  gh pr comment "$pr_url" --body "The workflow could not enable auto-merge on this pull request (GitHub refused it ${enable_retries} times). Merge it by hand once the checks are green." || true
  summary "- ${CONSUMER_REPO}: auto-merge could not be enabled on ${pr_url}"
  exit 0
fi
echo "auto-merge enabled on ${pr_url} (head ${head_oid:0:7})"

# --- Wait: merged, then deployed ---------------------------------------------
# One merge deploys to dev's single small node, and the matrix runs three jobs at
# once, so a job holds its slot until its service has rolled.
deadline=$(($(date +%s) + wait_seconds))
merge_sha=""
while :; do
  IFS=$'\t' read -r state merge_sha < <(gh pr view "$pr_url" --json state,mergeCommit \
    --jq '[.state, (.mergeCommit.oid // "")] | @tsv')
  case "$state" in
    MERGED) break ;;
    CLOSED) fail "${CONSUMER_REPO}: ${pr_url} was closed without a merge" ;;
  esac
  if [ "$(date +%s)" -ge "$deadline" ]; then
    summary "- ${CONSUMER_REPO}: ${pr_url} not merged within ${wait_seconds}s"
    fail "${CONSUMER_REPO}: ${pr_url} was not merged within ${wait_seconds}s — a required check is probably red"
  fi
  sleep "$poll_seconds"
done
echo "merged as ${merge_sha}"

appear_deadline=$(($(date +%s) + deploy_appear_seconds))
while :; do
  mapfile -t runs < <(gh run list -R "$CONSUMER_REPO" --workflow "$deploy_workflow" --commit "$merge_sha" \
    --limit 10 --json databaseId,status,conclusion \
    --jq '.[] | [.databaseId, .status, (.conclusion // "")] | @tsv')
  if [ "${#runs[@]}" -eq 0 ]; then
    if [ "$(date +%s)" -ge "$appear_deadline" ]; then
      notice "${CONSUMER_REPO}: no ${deploy_workflow} run for ${merge_sha:0:7} appeared in ${deploy_appear_seconds}s; not waiting for a deploy"
      summary "- ${CONSUMER_REPO}: merged (${merge_sha:0:7}), no deploy run seen"
      exit 0
    fi
  else
    pending=0
    bad=""
    for row in "${runs[@]}"; do
      IFS=$'\t' read -r run_id run_status run_conclusion <<<"$row"
      if [ "$run_status" != "completed" ]; then
        pending=1
      elif [ "$run_conclusion" != "success" ]; then
        bad+="${run_id}=${run_conclusion} "
      fi
    done
    if [ -n "$bad" ]; then
      summary "- ${CONSUMER_REPO}: merged (${merge_sha:0:7}), deploy failed: ${bad}"
      fail "${CONSUMER_REPO}: the ${deploy_workflow} run for ${merge_sha:0:7} did not succeed: ${bad}"
    fi
    if [ "$pending" -eq 0 ]; then
      echo "deploy of ${merge_sha:0:7} finished"
      summary "- ${CONSUMER_REPO}: merged and deployed (${merge_sha:0:7})"
      exit 0
    fi
  fi
  if [ "$(date +%s)" -ge "$deadline" ]; then
    fail "${CONSUMER_REPO}: the ${deploy_workflow} run for ${merge_sha:0:7} did not finish within ${wait_seconds}s"
  fi
  sleep "$poll_seconds"
done

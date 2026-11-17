#!/usr/bin/env bash
# Tests for scripts/ci/bump-consumer.sh and scripts/ci/auth-paths.sh.
#
# Hermetic: a local bare repository is the "consumer", a local git repository is
# "go-common", and `gh` and `go` are fakes on PATH that record every call. No
# network, no token, nothing leaves the temporary directory.
#
# Run: scripts/ci/bump_consumer_test.sh      (needs bash, git, jq)
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$here/../.." && pwd)
bump="$here/bump-consumer.sh"
authpaths="$here/auth-paths.sh"
workflow="$repo_root/.github/workflows/bump-consumers.yml"

root=$(mktemp -d)
trap '[ -n "${KEEP:-}" ] || rm -rf "$root"' EXIT

pass=0
failed=0
ok() {
  pass=$((pass + 1))
  echo "  ok   $*"
}
bad() {
  failed=$((failed + 1))
  echo "  FAIL $*"
}
check() { # check <description> <command...>
  local what=$1
  shift
  if "$@" >/dev/null 2>&1; then ok "$what"; else bad "$what"; fi
}

# --- the auth path list: the real one, out of the workflow ---------------------
extract_block() { # extract_block <KEY> <file>: the literal block under `  KEY: |`
  awk -v key="$1" '
    $0 ~ "^  " key ": \\|" { grab = 1; next }
    grab && /^    / { sub(/^    /, ""); print; next }
    grab && /^$/ { print; next }
    grab { exit }
  ' "$2"
}
AUTH_PATHS=$(extract_block AUTH_PATHS "$workflow")
AUTH_DEPS=$(extract_block AUTH_DEPS "$workflow")
export AUTH_PATHS AUTH_DEPS
if [ -z "$AUTH_PATHS" ]; then
  echo "could not read AUTH_PATHS out of $workflow"
  exit 1
fi

# --- fixtures ------------------------------------------------------------------
gitq() { git -c user.name=t -c user.email=t@example.com -c commit.gpgsign=false "$@"; }

make_gocommon() { # make_gocommon <dir>: five commits C1..C5, C3 and C5 are auth changes
  local d=$1
  git init -q -b main "$d"
  mkdir -p "$d/pkg/utils" "$d/pkg/errors" "$d/pkg/middleware" "$d/pkg/outbox"
  printf 'module github.com/STECH-Super-App/go-common\n\nrequire github.com/golang-jwt/jwt/v5 v5.3.0\n' >"$d/go.mod"
  echo a >"$d/pkg/utils/a.go"
  gitq -C "$d" add -A && gitq -C "$d" commit -q -m "feat: utils"
  C1=$(git -C "$d" rev-parse HEAD)
  echo b >"$d/pkg/errors/e.go"
  gitq -C "$d" add -A && gitq -C "$d" commit -q -m "feat: errors"
  C2=$(git -C "$d" rev-parse HEAD)
  echo c >"$d/pkg/middleware/auth.go"
  gitq -C "$d" add -A && gitq -C "$d" commit -q -m "fix(auth): middleware"
  C3=$(git -C "$d" rev-parse HEAD)
  echo d >"$d/pkg/outbox/o.go"
  gitq -C "$d" add -A && gitq -C "$d" commit -q -m "feat: outbox"
  C4=$(git -C "$d" rev-parse HEAD)
  sed -i 's#jwt/v5 v5.3.0#jwt/v5 v5.4.0#' "$d/go.mod"
  gitq -C "$d" add -A && gitq -C "$d" commit -q -m "build: bump jwt"
  C5=$(git -C "$d" rev-parse HEAD)
}

GC="$root/gocommon"
make_gocommon "$GC"

# The fakes. They read $FAKE for canned answers and write $FAKE/calls.log.
mkdir -p "$root/bin"
cat >"$root/bin/gh" <<'FAKEGH'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$FAKE/calls.log"
args=("$@")
jqexpr=""
for ((i = 0; i < ${#args[@]}; i++)); do
  case "${args[i]}" in
    --jq | -q) jqexpr=${args[i + 1]} ;;
    --body-file) cp "${args[i + 1]}" "$FAKE/pr_body.md" ;;
    --title) printf '%s' "${args[i + 1]}" >"$FAKE/pr_title" ;;
  esac
done
emit() { # emit <canned file>: what the real gh prints for --jq
  if [ -n "$jqexpr" ]; then jq -r "$jqexpr" <"$FAKE/$1"; else cat "$FAKE/$1"; fi
}
case "$*" in
  "api graphql"*) exit "${FAKE_GRAPHQL_RC:-0}" ;;
  "api -X DELETE "*) exit 0 ;;
  "api repos/"*"/compare/"*) emit compare.json ;;
  "api repos/"*"/branches/main/protection/required_status_checks"*)
    if [ -f "$FAKE/protection.json" ]; then emit protection.json; else
      cat "$FAKE/protection.err" 2>/dev/null >&2 || echo "gh: Not Found (HTTP 404)" >&2
      exit 1
    fi
    ;;
  "api repos/"*"/rules/branches/main"*) emit rules.json ;;
  "api repos/"*"/commits/"*)
    if [ "$jqexpr" = ".sha" ] && [ -f "$FAKE/commit_sha" ]; then cat "$FAKE/commit_sha"; else echo "subject from the API"; fi
    ;;
  "api repos/"*) emit repo.json ;;
  "pr list"*) emit open_prs.json ;;
  "pr create"*) echo "https://github.com/org/consumer/pull/7" ;;
  "pr view"*) emit pr_view.json ;;
  "pr close"* | "pr comment"*) exit 0 ;;
  "run list"*) emit runs.json ;;
  *)
    echo "fake gh: unexpected call: $*" >&2
    exit 99
    ;;
esac
FAKEGH
cat >"$root/bin/go" <<'FAKEGO'
#!/usr/bin/env bash
echo "go $* | KEY0=${GIT_CONFIG_KEY_0:-} VALUE0=${GIT_CONFIG_VALUE_0:-} GOPRIVATE=${GOPRIVATE:-}" >>"$FAKE/go.log"
case "$1" in
  get)
    [ -n "${FAKE_GO_NOCHANGE:-}" ] && exit 0
    mod=${2%@*}
    sha=${2#*@}
    sed -i -E "s#(${mod} )v[^ ]+#\1v0.0.0-20261117000000-${sha:0:12}#" go.mod
    ;;
  mod)
    [ -n "${FAKE_GO_NOCHANGE:-}" ] && exit 0
    echo "h1:new" >>go.sum
    [ -n "${FAKE_GO_STRAY:-}" ] && echo stray >"$FAKE_GO_STRAY"
    ;;
esac
exit 0
FAKEGO
chmod +x "$root/bin/gh" "$root/bin/go"

# new_case <name> [pin sha] [module dir]: a fresh consumer checkout and fresh canned answers.
new_case() {
  CASE="$root/case-$1"
  FAKE="$CASE/fake"
  REMOTE="$CASE/remote.git"
  CK="$CASE/checkout"
  local pin=${2:-$C3} dir=${3:-.} seed="$CASE/seed"
  mkdir -p "$FAKE"
  git init -q --bare -b main "$REMOTE"
  git init -q -b main "$seed"
  mkdir -p "$seed/$dir"
  printf 'module example.com/consumer\n\ngo 1.25.0\n\nrequire github.com/STECH-Super-App/go-common v0.0.0-20261116140524-%s\n' "${pin:0:12}" >"$seed/$dir/go.mod"
  echo "h1:old" >"$seed/$dir/go.sum"
  echo 'package main' >"$seed/$dir/main.go"
  gitq -C "$seed" add -A && gitq -C "$seed" commit -q -m init
  git -C "$seed" push -q "$REMOTE" main
  git clone -q "$REMOTE" "$CK"
  echo '{"allow_auto_merge":true,"allow_squash_merge":true}' >"$FAKE/repo.json"
  echo '{"contexts":["test / build & test","lint"],"checks":[{"context":"test / build & test"},{"context":"lint"}]}' >"$FAKE/protection.json"
  echo '[]' >"$FAKE/rules.json"
  echo '[]' >"$FAKE/open_prs.json"
  echo '{"id":"PR_node","state":"MERGED","mergeCommit":{"oid":"mmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmm"},"files":[{"path":"go.mod"},{"path":"go.sum"}]}' >"$FAKE/pr_view.json"
  echo '[{"databaseId":9,"status":"completed","conclusion":"success"}]' >"$FAKE/runs.json"
  : >"$FAKE/calls.log"
  : >"$FAKE/go.log"
  mk_compare "$pin" "$C4" ahead
}

mk_compare() { # mk_compare <from> <to> <status>
  git -C "$GC" log --format='%H%x09%s' "$1..$2" 2>/dev/null |
    jq -R -s --arg status "$3" '{
      status: $status,
      total_commits: (split("\n") | map(select(length > 0)) | length),
      commits: (split("\n") | map(select(length > 0) | split("\t") | {sha: .[0], html_url: ("https://github.com/x/commit/" + .[0]), commit: {message: .[1]}}))
    }' >"$FAKE/compare.json"
}

# run_bump <sha> [VAR=value ...]: runs the script, leaves its output in $CASE/out and its status in $RC.
run_bump() {
  local sha=$1
  shift
  RC=0
  env -i PATH="$root/bin:$PATH" HOME="$CASE" TMPDIR="${TMPDIR:-/tmp}" FAKE="$FAKE" \
    GO_COMMON_BUMP_TOKEN=tok GEN_GO_PAT=modpat \
    CONSUMER_REPO=org/consumer CONSUMER_DIR="$CK" MODULE_DIR=. \
    GOCOMMON_SHA="$sha" GOCOMMON_DIR="$GC" GOCOMMON_REPO=org/go-common \
    AUTH_PATHS="$AUTH_PATHS" AUTH_DEPS="$AUTH_DEPS" \
    REQUIRED_CHECKS='["test / build & test"]' DEPLOY_WORKFLOW=deploy.yml \
    POLL_SECONDS=0 ENABLE_RETRY_SECONDS=0 ENABLE_RETRIES=2 WAIT_SECONDS=2 DEPLOY_APPEAR_SECONDS=0 \
    PUSH_BEFORE="$C3" GITHUB_STEP_SUMMARY="$CASE/summary" \
    "$@" bash "$bump" >"$CASE/out" 2>&1 || RC=$?
}

called() { grep -q -- "$1" "$FAKE/calls.log"; }
not_called() { ! grep -q -- "$1" "$FAKE/calls.log"; }
remote_has_branch() { [ -n "$(git --git-dir="$REMOTE" branch --list "$1")" ]; }
out_has() { grep -q -- "$1" "$CASE/out"; }
body_has() { grep -q -- "$1" "$FAKE/pr_body.md"; }

# --- 1. a pure bump: PR opened, auto-merge armed, waits for merge and deploy ---
echo "pure bump"
new_case pure
run_bump "$C4"
check "exits 0" test "$RC" -eq 0
check "branch chore/go-common-${C4:0:7} pushed" remote_has_branch "chore/go-common-${C4:0:7}"
check "PR created" called "pr create"
check "PR title is 'chore: go-common <sha7> — <subject>'" grep -qxF "chore: go-common ${C4:0:7} — feat: outbox" "$FAKE/pr_title"
check "auto-merge mutation sent (SQUASH)" called "enablePullRequestAutoMerge"
check "body links the go-common commit" body_has "https://github.com/org/go-common/commit/${C4}"
check "body lists the commits since the previous pin" body_has "feat: outbox"
check "body says auto-merge is enabled" body_has "Auto-merge: enabled"
check "body says go build was not run" body_has "go build ./..."
check "the commit holds go.mod and go.sum only" test "$(git --git-dir="$REMOTE" show --name-only --format= "chore/go-common-${C4:0:7}" | sort | tr '\n' ' ')" = "go.mod go.sum "
check "go commands got the module-PAT insteadOf through the environment" grep -q "VALUE0=https://github.com/ " "$FAKE/go.log"
check "the PAT rewrite is not written to any git config" test -z "$(git -C "$CK" config --local --get-regexp 'url\..*insteadof' 2>/dev/null || true)"
check "waited for the deploy workflow run" called "run list -R org/consumer --workflow deploy.yml --commit mmmm"

# --- 2. the push range holds an auth commit: PR, no auto-merge -----------------
echo "auth-path commit"
new_case auth "$C1"
mk_compare "$C1" "$C4" ahead
run_bump "$C4" PUSH_BEFORE="$C2"
check "exits 0" test "$RC" -eq 0
check "PR created" called "pr create"
check "NO auto-merge call" not_called "enablePullRequestAutoMerge"
check "body names the auth rule" body_has "Critical Rule 20"
check "body names the commit" body_has "fix(auth): middleware"
check "the job did not wait for a merge" not_called "run list"

# --- 3. an auth commit older than the consumer's pin does not block ------------
echo "auth commit already behind the pin"
new_case behind-pin "$C3"
mk_compare "$C3" "$C4" ahead
run_bump "$C4" PUSH_BEFORE="$C3"
check "exits 0" test "$RC" -eq 0
check "auto-merge sent" called "enablePullRequestAutoMerge"

# --- 4. an auth commit between the pin and the push, push range clean ----------
echo "auth commit skipped by an earlier push"
new_case skipped "$C1"
mk_compare "$C1" "$C4" ahead
run_bump "$C4" PUSH_BEFORE="$C3"
check "no auto-merge (pin range holds C3)" not_called "enablePullRequestAutoMerge"
check "PR still opened" called "pr create"

# --- 5. anything besides go.mod / go.sum changed: refuse ------------------------
echo "stray file"
new_case stray
run_bump "$C4" FAKE_GO_STRAY=main.go
check "exits 1" test "$RC" -eq 1
check "nothing pushed" test -z "$(git --git-dir="$REMOTE" branch --list 'chore/*')"
check "no PR" not_called "pr create"
check "says why" out_has "not a pure bump"

# --- 6. already on that commit: nothing -----------------------------------------
echo "already on the commit"
new_case same "$C4"
run_bump "$C4"
check "exits 0" test "$RC" -eq 0
check "no PR" not_called "pr create"
check "nothing pushed" test -z "$(git --git-dir="$REMOTE" branch --list 'chore/*')"
check "go get not even run" test ! -s "$FAKE/go.log"

echo "go get changes nothing"
new_case nochange
printf 'module example.com/consumer\n\ngo 1.25.0\n\nrequire github.com/STECH-Super-App/go-common v1.2.3\n' >"$CK/go.mod"
gitq -C "$CK" commit -q -am tag
run_bump "$C4" FAKE_GO_NOCHANGE=1
check "exits 0, no PR" test "$RC" -eq 0 -a ! -s "$FAKE/calls.log"

echo "consumer already ahead"
new_case ahead "$C4"
mk_compare "$C4" "$C2" behind
run_bump "$C2"
check "no PR for an older commit" not_called "pr create"
check "exits 0" test "$RC" -eq 0

# --- 7. no token: notice, exit 0, nothing at all ---------------------------------
echo "no token"
new_case notoken
run_bump "$C4" GO_COMMON_BUMP_TOKEN=
check "exits 0" test "$RC" -eq 0
check "prints a notice" out_has "::notice::GO_COMMON_BUMP_TOKEN is not set"
check "no gh call at all" test ! -s "$FAKE/calls.log"
check "no go call" test ! -s "$FAKE/go.log"

# --- 8. an older open bump PR is closed and replaced -----------------------------
echo "older bump PR"
new_case older
echo '[{"number":5,"headRefName":"chore/go-common-abcdef0","url":"https://github.com/org/consumer/pull/5"},{"number":6,"headRefName":"feature/other","url":"x"}]' >"$FAKE/open_prs.json"
run_bump "$C4"
check "exits 0" test "$RC" -eq 0
check "old PR #5 closed with a comment" called "pr close 5 -R org/consumer --comment"
check "old branch deleted" called "api -X DELETE repos/org/consumer/git/refs/heads/chore/go-common-abcdef0"
check "an unrelated PR is left alone" not_called "pr close 6"
check "new PR created" called "pr create"
check "closed before the new PR was opened" test "$(grep -n 'pr close 5' "$FAKE/calls.log" | cut -d: -f1)" -lt "$(grep -n 'pr create' "$FAKE/calls.log" | cut -d: -f1)"

echo "the same bump is already open (a re-run)"
new_case rerun
echo "[{\"number\":7,\"headRefName\":\"chore/go-common-${C4:0:7}\",\"url\":\"https://github.com/org/consumer/pull/7\"}]" >"$FAKE/open_prs.json"
run_bump "$C4"
check "no second PR" not_called "pr create"
check "no PR closed" not_called "pr close"
check "auto-merge armed on the existing PR" called "enablePullRequestAutoMerge"

# --- 9. dry run -------------------------------------------------------------------
echo "dry run"
new_case dry
echo '[{"number":5,"headRefName":"chore/go-common-abcdef0","url":"https://github.com/org/consumer/pull/5"}]' >"$FAKE/open_prs.json"
run_bump "$C4" DRY_RUN=true
check "exits 0" test "$RC" -eq 0
check "nothing pushed" test -z "$(git --git-dir="$REMOTE" branch --list 'chore/*')"
check "no gh pr create" not_called "pr create"
check "no gh pr close" not_called "pr close"
check "no graphql" not_called "graphql"
check "no branch delete" not_called "DELETE"
check "prints what it would do" out_has "WOULD push branch chore/go-common-${C4:0:7}"
check "prints the closing it would do" out_has "WOULD close superseded bump PR #5"
check "prints the diff" out_has "go.mod"
check "no local branch or commit either" test "$(git -C "$CK" rev-parse --abbrev-ref HEAD)" = "main"

# --- 10. auto-merge stays off where the repo is not set up ------------------------
echo "repository without auto-merge"
new_case noauto
echo '{"allow_auto_merge":false,"allow_squash_merge":true}' >"$FAKE/repo.json"
run_bump "$C4"
check "PR opened" called "pr create"
check "no auto-merge call" not_called "graphql"
check "body says why" body_has "auto-merge is not allowed on the repository"
check "exits 0 without waiting" test "$RC" -eq 0 -a -z "$(grep 'run list' "$FAKE/calls.log" || true)"

echo "main without required checks"
new_case nochecks
rm "$FAKE/protection.json"
run_bump "$C4"
check "PR opened" called "pr create"
check "no auto-merge call" not_called "graphql"
check "body says why" body_has "no required status checks"

echo "the test job is not among the required checks"
new_case missing
echo '{"contexts":["lint"],"checks":[{"context":"lint"}]}' >"$FAKE/protection.json"
run_bump "$C4"
check "no auto-merge call" not_called "graphql"
check "body names the missing check" body_has "test / build & test"

echo "required checks from a ruleset"
new_case ruleset
rm "$FAKE/protection.json"
echo '[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"test / build & test"}]}}]' >"$FAKE/rules.json"
run_bump "$C4"
check "auto-merge armed" called "enablePullRequestAutoMerge"

echo "branch protection unreadable (403)"
new_case forbidden
rm "$FAKE/protection.json"
echo "gh: Resource not accessible by personal access token (HTTP 403)" >"$FAKE/protection.err"
run_bump "$C4" REQUIRED_CHECKS='[]'
check "fails closed: no auto-merge" not_called "graphql"
check "body says the token needs Administration: read" body_has "Administration: read"

echo "auto_merge false in the consumer list"
new_case flag
run_bump "$C4" AUTO_MERGE=false
check "PR opened" called "pr create"
check "no auto-merge call" not_called "graphql"
check "body names the flag" body_has "consumers.json"

echo "GitHub refuses to arm auto-merge"
new_case refused
run_bump "$C4" FAKE_GRAPHQL_RC=1
check "exits 0" test "$RC" -eq 0
check "tried twice" test "$(grep -c graphql "$FAKE/calls.log")" -eq 2
check "comments on the PR" called "pr comment"

# --- 11. the wait ---------------------------------------------------------------------
echo "PR never merges"
new_case nomerge
echo '{"id":"PR_node","state":"OPEN","mergeCommit":null,"files":[{"path":"go.mod"},{"path":"go.sum"}]}' >"$FAKE/pr_view.json"
run_bump "$C4" WAIT_SECONDS=1
check "exits 1 after the budget" test "$RC" -eq 1
check "says it was not merged" out_has "was not merged within"

echo "deploy fails"
new_case deployfail
echo '[{"databaseId":9,"status":"completed","conclusion":"failure"}]' >"$FAKE/runs.json"
run_bump "$C4"
check "exits 1" test "$RC" -eq 1

echo "no deploy run appears"
new_case nodeploy
echo '[]' >"$FAKE/runs.json"
run_bump "$C4"
check "exits 0 with a notice" test "$RC" -eq 0
check "says so" out_has "no deploy.yml run"

# --- 12. the PR is not pure at the last look -------------------------------------------
echo "PR files include something else"
new_case impure
echo '{"id":"PR_node","state":"OPEN","mergeCommit":null,"files":[{"path":"go.mod"},{"path":"main.go"}]}' >"$FAKE/pr_view.json"
run_bump "$C4"
check "exits 1" test "$RC" -eq 1
check "no auto-merge call" not_called "graphql"

# --- 13. a module in a subdirectory (sale-service) --------------------------------------
echo "module in a subdirectory"
new_case subdir "$C3" services/outbox-worker
echo '{"id":"PR_node","state":"MERGED","mergeCommit":{"oid":"mmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmm"},"files":[{"path":"services/outbox-worker/go.mod"},{"path":"services/outbox-worker/go.sum"}]}' >"$FAKE/pr_view.json"
run_bump "$C4" MODULE_DIR=services/outbox-worker
check "exits 0" test "$RC" -eq 0
check "only that module's go.mod and go.sum committed" test "$(git --git-dir="$REMOTE" show --name-only --format= "chore/go-common-${C4:0:7}" | sort | tr '\n' ' ')" = "services/outbox-worker/go.mod services/outbox-worker/go.sum "
check "auto-merge armed" called "enablePullRequestAutoMerge"

echo "a stray file next to the subdirectory module"
new_case subdir-stray "$C3" services/outbox-worker
run_bump "$C4" MODULE_DIR=services/outbox-worker FAKE_GO_STRAY=main.go
check "refuses (main.go is in the module dir, not go.mod/go.sum)" test "$RC" -eq 1

# --- 14. auth-paths.sh on its own --------------------------------------------------------
echo "auth-paths.sh"
rc_of() { # rc_of <from> <to>
  local rc=0
  bash "$authpaths" "$GC" "$1" "$2" >/dev/null 2>&1 || rc=$?
  echo "$rc"
}
check "clean range exits 0" test "$(rc_of "$C3" "$C4")" = 0
check "a middleware commit exits 10" test "$(rc_of "$C2" "$C3")" = 10
check "a jwt bump in go.mod exits 10" test "$(rc_of "$C4" "$C5")" = 10
check "an unknown revision exits 2" test "$(rc_of deadbeefdeadbeef "$C4")" = 2
check "an all-zero 'before' exits 2" test "$(rc_of 0000000000000000000000000000000000000000 "$C4")" = 2
check "empty AUTH_PATHS exits 2, never 'clean'" test "$(AUTH_PATHS='' rc_of "$C3" "$C4")" = 2

# One commit per listed area: each must trip the check.
AP="$root/ap"
git init -q -b main "$AP"
echo x >"$AP/README"
gitq -C "$AP" add -A && gitq -C "$AP" commit -q -m base
base=$(git -C "$AP" rev-parse HEAD)
for path in pkg/auth/claims.go pkg/authz/memberships.go pkg/middleware/auth.go pkg/trustedheaders/trustedheaders.go pkg/clientip/clientip.go; do
  mkdir -p "$AP/$(dirname "$path")"
  echo y >"$AP/$path"
  gitq -C "$AP" add -A && gitq -C "$AP" commit -q -m "touch $path"
  rc=0
  bash "$authpaths" "$AP" "$base" HEAD >/dev/null 2>&1 || rc=$?
  check "$path counts as an auth change" test "$rc" = 10
  git -C "$AP" reset -q --hard "$base"
done
for path in pkg/outbox/outbox.go pkg/events/events.go pkg/input/input.go pkg/notifyrender/x.go README.md; do
  mkdir -p "$AP/$(dirname "$path")"
  echo z >"$AP/$path"
  gitq -C "$AP" add -A && gitq -C "$AP" commit -q -m "touch $path"
  rc=0
  bash "$authpaths" "$AP" "$base" HEAD >/dev/null 2>&1 || rc=$?
  check "$path does not" test "$rc" = 0
  git -C "$AP" reset -q --hard "$base"
done

# --- 14b. plan-bump.sh -----------------------------------------------------------------------
echo "plan-bump.sh"
# run_plan [VAR=value ...]: the step's $GITHUB_OUTPUT lines land in $CASE/plan.out
run_plan() {
  RC=0
  : >"$CASE/plan.out"
  env -i PATH="$root/bin:$PATH" HOME="$CASE" FAKE="$FAKE" \
    GITHUB_OUTPUT="$CASE/plan.out" CONSUMERS_FILE="$repo_root/.github/consumers.json" \
    GOCOMMON_REPO=org/go-common BUMP_TOKEN=tok EVENT=push PUSH_SHA="$C4" \
    "$@" bash "$here/plan-bump.sh" >"$CASE/plan.log" 2>&1 || RC=$?
}
plan_out() { sed -n "s/^$1=//p" "$CASE/plan.out"; }
matrix_repos() { plan_out matrix | jq -r '[.include[].repo] | join(" ")'; }

new_case plan1
run_plan
check "push: exits 0" test "$RC" -eq 0
check "push: pins the pushed commit" test "$(plan_out sha)" = "$C4"
check "push: is not a dry run" test "$(plan_out dry_run)" = false
check "push: every enabled consumer" test "$(plan_out count)" = "$(jq '[.consumers[] | select(.enabled != false)] | length' "$repo_root/.github/consumers.json")"
check "push: geo-service is not in the matrix" test -z "$(matrix_repos | grep -w geo-service || true)"
check "push: sale-service carries its module dir" test "$(plan_out matrix | jq -r '.include[] | select(.repo == "sale-service") | .module_dir')" = services/outbox-worker
check "push: required checks travel as JSON" test "$(plan_out matrix | jq -r '.include[] | select(.repo == "seo-service") | .required_checks | fromjson | length')" = 2
check "push: auto_merge defaults to true" test "$(plan_out matrix | jq -r '.include[0].auto_merge')" = true

echo '{"consumers":[{"repo":"a","module_dir":".","auto_merge":false},{"repo":"b","module_dir":"."},{"repo":"c","module_dir":".","enabled":false}]}' >"$CASE/two.json"
run_plan CONSUMERS_FILE="$CASE/two.json"
check "push: auto_merge:false is kept, absent means true, enabled:false is dropped" test "$(plan_out matrix | jq -r '[.include[] | .repo + "=" + .auto_merge] | join(" ")')" = "a=false b=true"

new_case plan2
run_plan BUMP_TOKEN=
check "no token: exits 0 with an empty matrix" test "$RC" -eq 0 -a "$(plan_out count)" = 0
check "no token: says so" grep -q "GO_COMMON_BUMP_TOKEN is not set" "$CASE/plan.log"

new_case plan3
echo "$C4" >"$FAKE/commit_sha"
mk_compare "$C4" "$C5" ahead
run_plan EVENT=workflow_dispatch PUSH_SHA= INPUT_SHA= INPUT_ONLY= INPUT_DRY_RUN=
check "dispatch: dry run unless told otherwise" test "$(plan_out dry_run)" = true
check "dispatch: empty sha = head of main" test "$(plan_out sha)" = "$C4"
run_plan EVENT=workflow_dispatch PUSH_SHA= INPUT_SHA="${C4:0:7}" INPUT_ONLY=auth-service INPUT_DRY_RUN=false
check "dispatch: dry_run=false is honoured" test "$(plan_out dry_run)" = false
check "dispatch: only= keeps one consumer" test "$(plan_out count)" = 1 -a "$(matrix_repos)" = auth-service
run_plan EVENT=workflow_dispatch PUSH_SHA= INPUT_SHA= INPUT_ONLY=geo-service INPUT_DRY_RUN=false
check "dispatch: only= a disabled consumer opens nothing" test "$RC" -eq 0 -a "$(plan_out count)" = 0
run_plan EVENT=workflow_dispatch PUSH_SHA= INPUT_SHA= INPUT_ONLY=no-such-service
check "dispatch: unknown only= is an error" test "$RC" -eq 1
run_plan EVENT=workflow_dispatch PUSH_SHA= INPUT_SHA='main; echo injected'
check "dispatch: a sha that is not hex is an error" test "$RC" -eq 1
mk_compare "$C4" "$C5" diverged
run_plan EVENT=workflow_dispatch PUSH_SHA= INPUT_SHA="$C4"
check "dispatch: a commit that is not on main is refused" test "$RC" -eq 1

# --- 15. the consumer list -----------------------------------------------------------------
echo "consumers.json"
list="$repo_root/.github/consumers.json"
check "parses" jq -e '.consumers | length > 0' "$list"
check "every entry has repo, module_dir and required_checks" jq -e '[.consumers[] | has("repo") and has("module_dir") and has("required_checks")] | all' "$list"
check "no repo twice" jq -e '[.consumers[].repo] | (length == (unique | length))' "$list"
check "geo-service is explicitly switched off with a reason" jq -e '.consumers[] | select(.repo == "geo-service") | .enabled == false and (.note | length > 0)' "$list"
check "sale-service points at the outbox worker module" jq -e '.consumers[] | select(.repo == "sale-service") | .module_dir == "services/outbox-worker"' "$list"

echo
echo "${pass} passed, ${failed} failed"
[ "$failed" -eq 0 ]

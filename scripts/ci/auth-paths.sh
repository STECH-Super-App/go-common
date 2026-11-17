#!/usr/bin/env bash
# Does a range of go-common commits touch the token / auth-middleware code?
#
# Usage: auth-paths.sh <go-common checkout> <from> <to>
#
# Environment (both come from ONE place, the `env:` block of
# .github/workflows/bump-consumers.yml):
#   AUTH_PATHS  newline-separated path globs (`**` and `*` both match across `/`);
#               blank lines and lines starting with `#` are ignored.
#   AUTH_DEPS   newline-separated extended regexes; a changed line of go.mod in
#               the range that matches one counts as an auth change (the JWT
#               library decodes every token the platform accepts).
#
# Output: one line per finding on stdout.
# Exit:   0 nothing touched; 10 touched (Critical Rule 20: an auth change needs
#         the owner's sign-off and a real login on dev, so no auto-merge);
#         2 the range cannot be read. Callers treat 2 like 10: a range nobody
#         could check is never waved through.
set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo "usage: $0 <go-common checkout> <from> <to>" >&2
  exit 2
fi
dir=$1
from=$2
to=$3

if [ ! -d "$dir/.git" ] && ! git -C "$dir" rev-parse --git-dir >/dev/null 2>&1; then
  echo "go-common checkout not found at $dir"
  exit 2
fi
for rev in "$from" "$to"; do
  case "$rev" in
    '' | 0000000000000000000000000000000000000000)
      echo "no usable revision ('$rev')"
      exit 2
      ;;
  esac
  if ! git -C "$dir" rev-parse --verify --quiet "${rev}^{commit}" >/dev/null; then
    echo "revision $rev is not in the go-common checkout"
    exit 2
  fi
done

patterns=()
while IFS= read -r line; do
  [ -z "$line" ] && continue
  case "$line" in '#'*) continue ;; esac
  patterns+=("$line")
done <<<"${AUTH_PATHS:-}"

if [ "${#patterns[@]}" -eq 0 ]; then
  echo "AUTH_PATHS is empty: refusing to call a range clean"
  exit 2
fi

touched=0

while IFS= read -r commit; do
  [ -z "$commit" ] && continue
  subject=$(git -C "$dir" log -1 --format=%s "$commit")
  hit=""
  while IFS= read -r file; do
    [ -z "$file" ] && continue
    for pattern in "${patterns[@]}"; do
      # The pattern is a glob on purpose: case matches `*` across `/`.
      # shellcheck disable=SC2254
      case "$file" in
        $pattern)
          hit=$file
          break 2
          ;;
      esac
    done
  done < <(git -C "$dir" diff-tree --no-commit-id --name-only -r --root -m "$commit")
  if [ -n "$hit" ]; then
    echo "${commit:0:7} ${subject} (touches ${hit})"
    touched=1
  fi
done < <(git -C "$dir" rev-list --first-parent "${from}..${to}")

while IFS= read -r dep; do
  [ -z "$dep" ] && continue
  if git -C "$dir" diff "$from" "$to" -- go.mod | grep -E '^[+-][^+-]' | grep -Eq -- "$dep"; then
    echo "go.mod changes a dependency matching '${dep}' between ${from:0:7} and ${to:0:7}"
    touched=1
  fi
done <<<"${AUTH_DEPS:-}"

if [ "$touched" -eq 1 ]; then
  exit 10
fi
exit 0

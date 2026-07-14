#!/usr/bin/env bash
# Verifies every key in en.json exists in ru.json (top-level and nested)
# for the pkg/i18n error-string bundle — the file go-common's nightly
# Tolgee pull rewrites. Placeholder/param alignment for the notifyrender
# catalog is covered by the notifyrender unit tests in the Test workflow;
# this script only catches locale-key drift between en and non-en bundles.
set -euo pipefail

dir="pkg/i18n/translations"
en="${dir}/en.json"
jq -r 'paths(scalars) | join(".")' "$en" | sort > /tmp/en_keys.txt
for locale in ru; do
    target="${dir}/${locale}.json"
    jq -r 'paths(scalars) | join(".")' "$target" | sort > /tmp/${locale}_keys.txt
    if ! diff -q /tmp/en_keys.txt /tmp/${locale}_keys.txt >/dev/null; then
        echo "FAIL: key mismatch between en and ${locale}:"
        diff /tmp/en_keys.txt /tmp/${locale}_keys.txt
        exit 1
    fi
done
echo "OK: all locales have matching keys"

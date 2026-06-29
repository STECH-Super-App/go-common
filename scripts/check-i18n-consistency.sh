#!/usr/bin/env bash
# Verifies every key in en.json exists in ru.json (top-level
# and nested keys). The Go unit test (TestCatalogVsTemplateConsistency)
# checks placeholder alignment with requiredParams; this script catches
# missing keys in non-en locales (which the Go test misses if it only
# iterates en).
set -euo pipefail

en="pkg/notifyrender/translations/en.json"
jq -r 'paths(scalars) | join(".")' "$en" | sort > /tmp/en_keys.txt
for locale in ru; do
    target="pkg/notifyrender/translations/${locale}.json"
    jq -r 'paths(scalars) | join(".")' "$target" | sort > /tmp/${locale}_keys.txt
    if ! diff -q /tmp/en_keys.txt /tmp/${locale}_keys.txt >/dev/null; then
        echo "FAIL: key mismatch between en and ${locale}:"
        diff /tmp/en_keys.txt /tmp/${locale}_keys.txt
        exit 1
    fi
done
echo "OK: all locales have matching keys"

#!/usr/bin/env bash
# Verifies every [<section>] in en.toml exists in ru.toml and kk.toml.
# The Go unit test (TestCatalogVsTemplateConsistency) checks placeholder
# alignment with requiredParams; this script catches missing sections
# in non-en locales (which the Go test misses if it only iterates en).
set -euo pipefail

en="pkg/notifyrender/translations/en.toml"
for locale in ru kk; do
    target="pkg/notifyrender/translations/${locale}.toml"
    grep -oE '^\[[a-z_]+\]' "$en" | sort > /tmp/en_sections.txt
    grep -oE '^\[[a-z_]+\]' "$target" | sort > /tmp/${locale}_sections.txt
    if ! diff -q /tmp/en_sections.txt /tmp/${locale}_sections.txt >/dev/null; then
        echo "FAIL: section mismatch between en and ${locale}:"
        diff /tmp/en_sections.txt /tmp/${locale}_sections.txt
        exit 1
    fi
done
echo "OK: all locales have matching sections"

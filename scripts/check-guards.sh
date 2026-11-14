#!/usr/bin/env bash
# Proves that every guard in .golangci.strict.yml still FIRES: lints the
# deliberately bad sample in pkg/lintguards/testdata/violations and fails when a
# rule's message is missing from the output. A guard that silently stops
# matching (a golangci-lint or forbidigo upgrade changed how a pattern is read —
# it has happened: the base file's `^c\.JSON$` never matched a call) would
# otherwise look exactly like a clean service.
#
# Usage: scripts/check-guards.sh [path to golangci-lint]   (default: golangci-lint)
set -euo pipefail

cd "$(dirname "$0")/.."
LINT="${1:-golangci-lint}"
SAMPLE=./pkg/lintguards/testdata/violations/

out="$("$LINT" run --allow-parallel-runners -c .golangci.strict.yml "$SAMPLE" 2>&1 || true)"

# One fragment of each rule's message; they are what a developer reads.
expected=(
  "go-common/pkg/kafkawriter"            # kafka.NewWriter
  "go-common/pkg/httpserver.New"         # echo.New
  "go-common/pkg/lifecycle"              # signal.Notify
  "standard log package"                 # depguard: log
  "client-address helper"                # c.RealIP
  "the gateway sets X-Client-IP"         # echo.HeaderXForwardedFor
  "r.RemoteAddr is the TCP peer"         # http.Request.RemoteAddr
  "read the client address with go-common/pkg/clientip"  # auth.HeaderRealIP
  "reserved for api-gateway"             # clientip.Set
  "go-common/pkg/egress"                 # http.DefaultClient
  "grpcopts.NewClient"                   # grpc.NewClient
  "go-common/pkg/response"               # c.JSON
)

missing=0
for fragment in "${expected[@]}"; do
  if ! grep -qF -- "$fragment" <<<"$out"; then
    echo "GUARD DID NOT FIRE: no finding mentioning \"$fragment\"" >&2
    missing=1
  fi
done

if [ "$missing" -ne 0 ]; then
  echo "---- golangci-lint output ----" >&2
  echo "$out" >&2
  exit 1
fi
echo "all ${#expected[@]} guards fire on the violations sample"

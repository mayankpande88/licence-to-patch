#!/usr/bin/env bash
# Build and launch the three Licence to Patch MCP servers on loopback.
# The write servers read the GitHub token from a gitignored file — it never
# reaches the model or the sandbox. Register each URL as a No-auth MCP
# connector in TrueForge (Settings -> Connectors), then create the agent from
# ../agent.json (see README "Run the agent").
set -euo pipefail
cd "$(dirname "$0")/.."
TOKEN_FILE="${GITHUB_TOKEN_FILE:-$HOME/.config/lp/github_token}"
[ -f "$TOKEN_FILE" ] || { echo "missing token file: $TOKEN_FILE (fine-grained PAT: Contents read, Pull requests read/write)"; exit 1; }
TOKEN="$(cat "$TOKEN_FILE")"

go build -o /tmp/depdiff-mcp ./cmd/depdiff-mcp
go build -o /tmp/review-mcp  ./cmd/review-mcp
go build -o /tmp/fix-mcp     ./cmd/fix-mcp

/tmp/depdiff-mcp -addr 127.0.0.1:8971 &                 # read-only, ungated
GITHUB_TOKEN="$TOKEN" /tmp/review-mcp -addr 127.0.0.1:8972 &  # posts the trust brief (advisory)
GITHUB_TOKEN="$TOKEN" /tmp/fix-mcp    -addr 127.0.0.1:8973 &  # changes code (gated by the agent policy)
echo "depdiff :8971  review :8972  fix :8973  — endpoints /mcp"
wait

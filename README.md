# Licence to Patch

A dependency-update **trust agent**, built on the open-source [TrueForge](https://github.com/truefoundry/trueforge) harness.

Dependabot and Renovate open a flood of update PRs, and grouped PRs bundle several bumps at once. Teams rubber-stamp or ignore them because they can't tell which bump is safe. The dangerous ones aren't the updates that break your tests — CI already catches those. They're the **behavioral / contract changes that pass green CI and break production**: a changed default, a new retry policy, a baked-in REST api-version the live service rejects.

*Licence to Patch* reviews a (grouped) Dependabot PR, tells you which bump you can trust, and — only behind a human approval gate — fixes the one you can't.

> Licence to patch — but only with your signature.

## The wedge: "green CI ≠ safe"

The flagship detector diffs a dependency's **source** between the current and target versions and surfaces contract changes the changelog omits. The motivating case is real: `armmonitor` (Azure SDK for Go) v0.12.0 → v0.13.0 silently changed the Azure Monitor metric-alert REST api-version from `2024-03-01-preview` to `2026-01-01`, which ARM rejects at runtime — invisible to the compiler, a mocked test suite, reachability scanners, and the changelog alike. Only a source diff between the two versions reveals it.

## What it does

Given a grouped Dependabot PR, the agent:

1. **Reads the PR** (GitHub MCP) and lists every bump.
2. **Analyses each bump.** For Go it runs `depdiff` (source diff → changed api-version literals, removed exported symbols). For other ecosystems it does the equivalent in the sandbox.
3. **Verifies in the sandbox** that the bumped code still builds and its tests pass — proving CI *would* be green, live.
4. **Posts a per-bump trust brief** as a PR comment — ACCEPT / CAUTION / HOLD, each with *what changed / why it matters / recommendation*. This is advisory, so it posts without stopping.
5. **Fixes the unsafe bump — behind a human gate.** For a HOLD it proposes a concrete fix; the harness pauses for your Allow/Deny before it changes anything, then either drives Dependabot to rebuild the group without the module, or reverts the bump on the branch directly.

The approval gate is on the **fix**, not the comment: posting a review is advice; changing the code is the irreversible act.

## Architecture

```
                 TrueForge harness (loop · approvals · sandbox · session · subagents)
                                          │
     ┌───────────┬───────────┬───────────┼───────────────┬──────────────────────┐
 github (MCP)  depdiff(MCP) sandbox    review (MCP)    fix (MCP)             verdict/brief
 read the PR   diff a Go    go build   post the trust  revert_bump_on_pr     deterministic
 & its bumps   dep's source + go test  brief comment   (edit branch) OR      ACCEPT/CAUTION/
               between two  (live      (ADVISORY —     hold_via_dependabot   HOLD + detailed
               versions     green CI)  ungated)        (@dependabot ...)     markdown
                                                       → GATED (human
                                                         approves the fix)
```

The custom code is small; the harness does the heavy lifting (tool loop, sandboxed execution, the human-approval pause, subagents, session state).

## How the approval gate is placed

The gate belongs on the code-changing fix, not on posting a comment. The right lever in TrueForge is **per-connector `require_approval_for_tools`** in the agent spec — not tool annotations. (An un-annotated MCP tool does *not* become ungated: MCP defaults tools to `destructiveHint: true`, so it would still pause.) The provided saved agent gates only the `fix` connector and leaves `review` / `depdiff` / `github` ungated:

```jsonc
"mcp_servers": [
  { "name": "depdiff", "require_approval_for_tools": [] },
  { "name": "github",  "require_approval_for_tools": [] },
  { "name": "review",  "require_approval_for_tools": [] },   // advisory comment — no gate
  { "name": "fix",     "require_approval_for_tools": ["@all"] } // code change — gated
]
```

## Components

- `cmd/depdiff-mcp` — MCP server: `diff_go_dependency(module, from, to)`. Downloads both versions, diffs the source, returns findings + a preliminary verdict + a review-ready markdown explanation. Read-only.
- `cmd/review-mcp` — MCP server: `post_pr_review(owner, repo, pull_number, event, body)`. Advisory (ungated via the agent's policy).
- `cmd/fix-mcp` — MCP server, the gated code-changing actions: `revert_bump_on_pr` (clone the PR branch, pin the module back, `go mod tidy`, commit, push) and `hold_via_dependabot` (comment `@dependabot ignore <module>` + `@dependabot recreate`).
- `internal/depdiff` · `internal/verdict` · `internal/brief` — detection core, ACCEPT/CAUTION/HOLD classifier, and the *what/why/recommendation* renderer.
- `internal/ghreview` · `internal/fix` — GitHub reviews/comments client and the branch-revert logic.
- `tools/depdiff` — the detection core as a CLI.

Every write server binds to loopback, reads its token from `GITHUB_TOKEN` (never the model or sandbox), validates inputs, and refuses a non-loopback bind without `-allow-remote`.

## Multi-language

The deterministic `depdiff` tool targets Go, where the flagship api-version detector lives. The *technique* — "diff the source, not the changelog" — is language-agnostic, and the agent applies it in the sandbox to any ecosystem. Two full demos plus a live proof:

- **Go** — [`azmetrics-demo`](https://github.com/mayankpande88/azmetrics-demo): `armmonitor` 0.12 → 0.13 flips a baked-in api-version ARM rejects. Analysed by the `depdiff` tool.
- **Python** — [`pyconfig-demo`](https://github.com/mayankpande88/pyconfig-demo): `PyYAML` 5.3 → 6.0 makes `yaml.load`'s `Loader` argument required, so `load_config` raises `TypeError` at runtime while the tests (which cover a different function) stay green. Analysed by the agent in the sandbox.
- **npm / React** (live technique proof) — `node-fetch` 2 → 3 goes ESM-only (`"type": "module"`), so `require()` throws `ERR_REQUIRE_ESM` at runtime.

## Try the detector (no setup)

```
go run ./tools/depdiff github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor v0.12.0 v0.13.0
```

You'll see the api-version change in `metricalerts_client.go` (`2024-03-01-preview` → `2026-01-01`), a HOLD verdict, and the review-ready explanation.

## Run the agent (TrueForge)

1. Start TrueForge: `npx @truefoundry/trueforge` (Node ≥ 22.14) → http://localhost:8790
2. Settings → Models: add a provider (any — it's model-neutral). Settings → Sandbox providers: add Daytona.
3. Start the MCP servers (each on loopback):
   ```
   go run ./cmd/depdiff-mcp -addr 127.0.0.1:8971
   GITHUB_TOKEN=… go run ./cmd/review-mcp -addr 127.0.0.1:8972
   GITHUB_TOKEN=… go run ./cmd/fix-mcp    -addr 127.0.0.1:8973
   ```
4. Settings → Connectors: add each MCP URL (`http://localhost:897{1,2,3}/mcp`, No auth), and the shipped **github** connector with a fine-grained PAT (Contents: read, Pull requests: read & write).
5. Create the saved agent with the approval policy above (`POST /api/v1/agents`, or the Agents Library UI). Open a session on it and ask it to review a grouped Dependabot PR. The trust brief posts; the fix pauses for your approval.

## Demo targets

- [mayankpande88/azmetrics-demo](https://github.com/mayankpande88/azmetrics-demo) — Go, the `armmonitor` api-version landmine.
- [mayankpande88/pyconfig-demo](https://github.com/mayankpande88/pyconfig-demo) — Python, the `PyYAML` `yaml.load` landmine.

Both ship a real grouped Dependabot PR whose green test suite is blind to the breaking change.

---

Built for the WeMakeDevs × TrueFoundry Agent Harness Hackathon. MIT-licensed, open source.

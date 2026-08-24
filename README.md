# Licence to Patch

A dependency-update **trust agent**, built on the open-source [TrueForge](https://github.com/truefoundry/trueforge) harness.

Dependabot and Renovate open a flood of update PRs, and grouped PRs bundle several bumps at once. Teams rubber-stamp or ignore them because they can't tell which bump is safe. The dangerous ones aren't the updates that break your tests — CI already catches those. They're the **behavioral / contract changes that pass green CI and break production**: a changed default, a new retry policy, a baked-in REST api-version the live service rejects.

*Licence to Patch* reviews a (grouped) Dependabot PR and, for each bump, decides whether you can trust it — then acts only behind a human approval gate.

> Licence to patch — but only with your signature.

## The wedge: "green CI ≠ safe"

The flagship detector diffs a dependency's **source** between the current and target versions and surfaces contract changes the changelog omits. The motivating case is real: `armmonitor` (Azure SDK for Go) v0.12.0 → v0.13.0 silently changed the Azure Monitor metric-alert REST api-version from `2024-03-01-preview` to `2026-01-01`, which ARM rejects at runtime — invisible to the compiler, a mocked test suite, reachability scanners, and the changelog alike. Only a source diff between the two versions reveals it.

## What it does

Given a grouped Dependabot PR, the agent:

1. **Reads the PR** (GitHub MCP) and extracts every module bump.
2. **Diffs each dependency's source** between versions (`depdiff`) and flags contract changes — changed api-version literals, removed exported symbols.
3. **Verifies in the sandbox** that the bumped code still builds and its tests pass — proving CI would be green, live.
4. **Writes a per-module verdict** — ACCEPT / CAUTION / HOLD — each with a *what changed / why it matters / recommendation* explanation.
5. **Posts a gated review.** Requesting changes on a real PR is annotated destructive, so TrueForge pauses for a human Allow/Deny before it fires.

On the demo PR it recommends: merge the safe bumps, **hold** the one that changes the api-version, and split the group.

## Architecture

```
                       TrueForge harness (loop · approvals · sandbox · session)
                                          │
        ┌──────────────┬─────────────────┼──────────────────┬────────────────┐
   github (MCP)    depdiff (MCP)     sandbox (Daytona)   review (MCP)       verdict/brief
   read the PR,    diff a Go dep's   go build + go test  post a PR review   deterministic
   its bumps       source between    on the bumped code  COMMENT / REQUEST_ ACCEPT/CAUTION/
   & diff          two versions →    (live "green CI")   CHANGES / APPROVE  HOLD + detailed
                   api-version /                         → annotated        markdown
                   removed symbols                       DESTRUCTIVE,
                   + verdict + md                        harness-gated
```

The custom code is small; the harness does the heavy lifting (tool loop, sandboxed execution, the human-approval pause, session state).

## Components

- `cmd/depdiff-mcp` — MCP server exposing `diff_go_dependency(module, from, to)`. Downloads both versions, diffs the source, returns the findings, a preliminary verdict, and a review-ready markdown explanation. Read-only.
- `cmd/review-mcp` — MCP server exposing `post_pr_review(owner, repo, pull_number, event, body)`. Annotated **destructive** so the harness gates it. Binds to loopback by default; token comes from `GITHUB_TOKEN` and never reaches the model or sandbox.
- `internal/depdiff` — the source-diff detection core.
- `internal/verdict` — deterministic ACCEPT / CAUTION / HOLD classification.
- `internal/brief` — renders each finding as a *what / why / recommendation* markdown section.
- `internal/ghreview` — thin GitHub reviews client.
- `tools/depdiff` — the detection core as a CLI.

## Multi-language

The deterministic `depdiff` tool targets Go, where the flagship api-version detector lives. The *technique* — "diff the source, not the changelog" — is language-agnostic, and the agent applies it in the sandbox to any ecosystem. Demonstrated live:

- **Python** — `pyyaml` 5.3 → 5.3.1: the constructor added `check_state_key`; identical API signatures, changed internal validation → passes CI, breaks at runtime for code that relied on the old behavior.
- **npm / React** — `node-fetch` 2 → 3: `package.json` added `"type": "module"` (ESM-only), removed the CJS entry → `require()` throws `ERR_REQUIRE_ESM` at runtime.

## Try the detector

```
go run ./tools/depdiff github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor v0.12.0 v0.13.0
```

You'll see the api-version change in `metricalerts_client.go` (`2024-03-01-preview` → `2026-01-01`), a HOLD verdict, and the review-ready explanation.

## Run the agent (TrueForge)

1. Start TrueForge: `npx @truefoundry/trueforge` (Node ≥ 22.14) → http://localhost:8790
2. Settings → Models: add a provider (any — it's model-neutral).
3. Settings → Sandbox providers: add Daytona.
4. Start the MCP servers and connect them under Settings → Connectors:
   ```
   go run ./cmd/depdiff-mcp -addr 127.0.0.1:8971        # → http://localhost:8971/mcp (No auth)
   GITHUB_TOKEN=… go run ./cmd/review-mcp -addr 127.0.0.1:8972   # → http://localhost:8972/mcp (No auth)
   ```
   Also connect the shipped **github** connector with a fine-grained PAT (Contents: read, Pull requests: read & write).
5. Open a chat with the three connectors enabled and ask it to review a grouped Dependabot PR.

## Demo target

[mayankpande88/azmetrics-demo](https://github.com/mayankpande88/azmetrics-demo) — a clean-room Go service whose green test suite is blind to the `armmonitor` api-version regression, with a real grouped Dependabot PR to review.

---

Built for the WeMakeDevs × TrueFoundry Agent Harness Hackathon. MIT-licensed, open source.

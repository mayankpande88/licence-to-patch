# Licence to Patch

A dependency-update **trust agent**, built on the [TrueForge](https://github.com/truefoundry/trueforge) harness.

Dependabot and Renovate open a flood of update PRs; teams rubber-stamp or ignore them
because they can't tell which bump is safe. The dangerous ones aren't the updates that
break your tests — CI already catches those. They're the **behavioral / contract changes
that pass green CI and break production**: a changed default, a new retry policy, a baked-in
REST api-version the live service rejects.

*Licence to Patch* reviews a (grouped) Dependabot PR, and for each bump assembles a **trust
brief** — what actually changed in the dependency's source (not just its changelog), whether
your code hits it, whether your tests cover it — then gives a verdict (**accept / caution /
HOLD**) and acts only behind a **human approval gate**.

> Licence to patch — but only with your signature.

## The wedge: "green CI ≠ safe"

The headline detector diffs a dependency's **source** between the current and target versions
and surfaces contract changes the changelog omits. Motivating real case: `armmonitor`
v0.12.0 → v0.13.0 silently flips the Azure Monitor metric-alert api-version from
`2024-03-01-preview` to `2026-01-01`, which ARM rejects at runtime — invisible to the compiler,
a mocked test suite, reachability tools, and the changelog alike.

## Components

- `tools/depdiff` — the detection core. Downloads two versions of a Go module and reports
  contract-relevant diffs (changed api-version literals, removed exported symbols). Try:
  ```
  go run ./tools/depdiff github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor v0.12.0 v0.13.0
  ```

More components (MCP servers, TrueForge agent spec, skills, the trust-brief UI) land as the
build progresses.

## Demo target

[mayankpande88/azmetrics-demo](https://github.com/mayankpande88/azmetrics-demo) — a clean-room
Go service whose green test suite is blind to the armmonitor api-version regression.

---

Built for the WeMakeDevs × TrueFoundry Agent Harness Hackathon. MIT-licensed, open source.

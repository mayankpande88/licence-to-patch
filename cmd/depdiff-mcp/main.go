// Command depdiff-mcp exposes the depdiff detection core as an MCP server over
// streamable HTTP, so a TrueForge agent can call it as a real tool.
//
// Tool: diff_go_dependency(module, from_version, to_version) -> JSON evidence of
// contract-relevant changes (api-version literal changes, removed exported
// symbols) that a changelog omits and that mocked tests do not catch, plus a
// preliminary verdict as a hint. It returns facts, not a finished review — the
// agent reasons over them and writes the recommendation.
//
// The tool is annotated read-only so the harness runs it autonomously.
//
// Usage:
//
//	depdiff-mcp [-addr :8971]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/mayankpande88/licence-to-patch/internal/depdiff"
	"github.com/mayankpande88/licence-to-patch/internal/verdict"
)

// toolResult is the deterministic evidence the tool gives the agent: the raw
// source-diff facts (changed constant values, an api-version heuristic, removed
// exported functions, files-changed count) plus a preliminary verdict as a hint.
//
// It deliberately does NOT render a review-ready recommendation. Writing the
// per-repo brief — reasoning about which changes this repo actually reaches,
// weighing the test result, and recommending options — is the agent's job, not
// the tool's. Handing the agent finished prose only anchors it to echo. The
// standalone CLI (tools/depdiff), which has no agent, keeps its own renderer.
type toolResult struct {
	depdiff.Report
	Preliminary verdict.Verdict `json:"preliminary"`
}

func main() {
	addr := flag.String("addr", ":8971", "listen address for the streamable-HTTP MCP server")
	flag.Parse()

	// 0.3.0: renamed the result field removed_exported_symbols ->
	// removed_exported_functions (it only ever held functions) and added
	// changed_constants (AST const-value diff). 0.2.0 had dropped `markdown`.
	// Bumped per semver so the contract change is visible to clients.
	s := server.NewMCPServer(
		"licence-to-patch-depdiff",
		"0.3.0",
		server.WithToolCapabilities(true),
	)

	tool := mcp.NewTool(
		"diff_go_dependency",
		mcp.WithDescription(
			"Diff the SOURCE of a Go module between two versions and report contract-relevant "+
				"changes that a changelog omits and that a mocked test suite cannot catch: changed "+
				"package-level constant VALUES (any baked-in value — a timeout, endpoint, retry count, "+
				"enum, or REST api-version), removed exported functions, and the count of changed .go "+
				"files. (The api-version signal is a narrow, high-precision heuristic; the constant-value "+
				"diff is the general one.) Also returns a deterministic preliminary verdict "+
				"(ACCEPT/CAUTION/HOLD) as a hint. This is EVIDENCE, not a finished review: reason over "+
				"these facts yourself — search for where this repo uses the changed symbols, run the "+
				"tests, and write the recommendation. Use this to decide whether a dependency bump is safe.",
		),
		mcp.WithString("module", mcp.Required(),
			mcp.Description("Go module path, e.g. github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor")),
		mcp.WithString("from_version", mcp.Required(),
			mcp.Description("Current version, e.g. v0.12.0")),
		mcp.WithString("to_version", mcp.Required(),
			mcp.Description("Proposed version, e.g. v0.13.0")),
		// Read-only: the harness runs this without asking for approval.
		mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: mcp.ToBoolPtr(true)}),
	)

	s.AddTool(tool, handleDiff)

	log.Printf("depdiff MCP server (streamable HTTP) listening on %s (endpoint /mcp)", *addr)
	httpServer := server.NewStreamableHTTPServer(s)
	if err := httpServer.Start(*addr); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func handleDiff(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	module, err := req.RequireString("module")
	if err != nil {
		return mcp.NewToolResultError("module is required"), nil
	}
	from, err := req.RequireString("from_version")
	if err != nil {
		return mcp.NewToolResultError("from_version is required"), nil
	}
	to, err := req.RequireString("to_version")
	if err != nil {
		return mcp.NewToolResultError("to_version is required"), nil
	}

	rep, err := depdiff.Diff(module, from, to)
	if err != nil {
		return mcp.NewToolResultErrorf("diff failed: %v", err), nil
	}
	v := verdict.Classify(rep)
	res := toolResult{Report: rep, Preliminary: v}
	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return mcp.NewToolResultErrorf("marshal failed: %v", err), nil
	}
	return mcp.NewToolResultText(string(out)), nil
}

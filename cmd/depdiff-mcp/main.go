// Command depdiff-mcp exposes the depdiff detection core as an MCP server over
// streamable HTTP, so a TrueForge agent can call it as a real tool.
//
// Tool: diff_go_dependency(module, from_version, to_version) -> JSON report of
// contract-relevant changes (api-version literal changes, removed exported
// symbols) that a changelog omits and that mocked tests do not catch.
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

// toolResult is the depdiff report plus a deterministic preliminary verdict.
// The verdict is a hint; the agent and the approving human make the final call.
type toolResult struct {
	depdiff.Report
	Preliminary verdict.Verdict `json:"preliminary"`
}

func main() {
	addr := flag.String("addr", ":8971", "listen address for the streamable-HTTP MCP server")
	flag.Parse()

	s := server.NewMCPServer(
		"licence-to-patch-depdiff",
		"0.1.0",
		server.WithToolCapabilities(true),
	)

	tool := mcp.NewTool(
		"diff_go_dependency",
		mcp.WithDescription(
			"Diff the SOURCE of a Go module between two versions and report contract-relevant "+
				"changes that a changelog omits and that a mocked test suite cannot catch: changed "+
				"REST api-version literals (a runtime contract change), removed exported symbols, and "+
				"the count of changed .go files. Also returns a deterministic preliminary verdict "+
				"(ACCEPT/CAUTION/HOLD) as a hint. Use this to decide whether a dependency bump is safe.",
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
	res := toolResult{Report: rep, Preliminary: verdict.Classify(rep)}
	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return mcp.NewToolResultErrorf("marshal failed: %v", err), nil
	}
	return mcp.NewToolResultText(string(out)), nil
}

// Command fix-mcp exposes the one irreversible action — rewriting a PR's code —
// as a gated MCP tool over streamable HTTP.
//
// revert_bump_on_pr pins a flagged dependency back to a safe version on the PR
// branch and pushes. It is annotated DESTRUCTIVE, so the TrueForge harness pauses
// for human approval before it runs: the agent proposes the fix, a person signs
// off, then the branch changes. Posting a review is advice and needs no gate;
// changing the code does.
//
// The GitHub token comes from GITHUB_TOKEN and stays in this process.
//
// Usage:
//
//	GITHUB_TOKEN=... fix-mcp [-addr 127.0.0.1:8973]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/mayankpande88/licence-to-patch/internal/fix"
	"github.com/mayankpande88/licence-to-patch/internal/ghreview"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8973", "listen address for the streamable-HTTP MCP server")
	allowRemote := flag.Bool("allow-remote", false, "permit binding to a non-loopback address (unauthenticated — do not use without an auth proxy)")
	flag.Parse()

	if !*allowRemote && !isLoopback(*addr) {
		log.Fatalf("refusing to bind %q: this server has no authentication and rewrites PR branches; "+
			"keep it on loopback, or pass -allow-remote only behind an auth proxy", *addr)
	}

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		log.Fatal("GITHUB_TOKEN is required")
	}
	fixer := fix.New(token)

	s := server.NewMCPServer("licence-to-patch-fix", "0.1.0", server.WithToolCapabilities(true))

	tool := mcp.NewTool(
		"revert_bump_on_pr",
		mcp.WithDescription(
			"Fix a grouped dependency PR by reverting ONE unsafe module bump on the PR branch: "+
				"pin the module back to a safe version, run go mod tidy, commit, and push. This "+
				"rewrites the PR's code and is irreversible, so it pauses for human approval first. "+
				"The safe bumps in the group stay.",
		),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Repository owner")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository name")),
		mcp.WithString("branch", mcp.Required(), mcp.Description("The PR head branch (e.g. dependabot/go_modules/…)")),
		mcp.WithString("module", mcp.Required(), mcp.Description("The module path to revert")),
		mcp.WithString("safe_version", mcp.Required(), mcp.Description("The version to pin back to, e.g. v0.12.0")),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			ReadOnlyHint:    mcp.ToBoolPtr(false),
			DestructiveHint: mcp.ToBoolPtr(true),
		}),
	)

	s.AddTool(tool, handler(fixer))

	// Alternative fix for a Dependabot PR: ask Dependabot to drop the module and
	// rebuild the group, instead of editing the branch ourselves. Also gated —
	// it triggers a real PR rewrite.
	gh := ghreview.New(token)
	dependabotTool := mcp.NewTool(
		"hold_via_dependabot",
		mcp.WithDescription(
			"Fix a grouped Dependabot PR the idiomatic way: comment `@dependabot ignore <module>` "+
				"and `@dependabot recreate` so Dependabot rebuilds the group WITHOUT the unsafe module. "+
				"Note: ignore also stops future updates to that module until unignored. Triggers a PR "+
				"rewrite, so it pauses for human approval first.",
		),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Repository owner")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository name")),
		mcp.WithNumber("pull_number", mcp.Required(), mcp.Description("Pull request number")),
		mcp.WithString("module", mcp.Required(), mcp.Description("The module path to drop from the group")),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			ReadOnlyHint:    mcp.ToBoolPtr(false),
			DestructiveHint: mcp.ToBoolPtr(true),
		}),
	)
	s.AddTool(dependabotTool, dependabotHandler(gh))

	log.Printf("fix MCP server (streamable HTTP) listening on %s (endpoint /mcp)", *addr)
	if err := server.NewStreamableHTTPServer(s).Start(*addr); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func handler(fixer *fix.Fixer) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		get := func(k string) (string, *mcp.CallToolResult) {
			v, err := req.RequireString(k)
			if err != nil {
				return "", mcp.NewToolResultErrorf("invalid %q: %v", k, err)
			}
			return v, nil
		}
		owner, e1 := get("owner")
		repo, e2 := get("repo")
		branch, e3 := get("branch")
		module, e4 := get("module")
		safe, e5 := get("safe_version")
		for _, e := range []*mcp.CallToolResult{e1, e2, e3, e4, e5} {
			if e != nil {
				return e, nil
			}
		}

		res, err := fixer.RevertBump(ctx, owner, repo, branch, module, safe)
		if err != nil {
			return mcp.NewToolResultErrorf("revert failed: %v", err), nil
		}
		out, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return mcp.NewToolResultErrorf("marshal failed: %v", err), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	}
}

func dependabotHandler(gh *ghreview.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, err := req.RequireString("owner")
		if err != nil {
			return mcp.NewToolResultErrorf("invalid 'owner': %v", err), nil
		}
		repo, err := req.RequireString("repo")
		if err != nil {
			return mcp.NewToolResultErrorf("invalid 'repo': %v", err), nil
		}
		number, err := req.RequireInt("pull_number")
		if err != nil || number <= 0 {
			return mcp.NewToolResultError("invalid 'pull_number'"), nil
		}
		module, err := req.RequireString("module")
		if err != nil {
			return mcp.NewToolResultErrorf("invalid 'module': %v", err), nil
		}

		body := fmt.Sprintf("@dependabot ignore %s\n@dependabot recreate", module)
		res, err := gh.PostComment(ctx, owner, repo, number, body)
		if err != nil {
			return mcp.NewToolResultErrorf("dependabot comment failed: %v", err), nil
		}
		out, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return mcp.NewToolResultErrorf("marshal failed: %v", err), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	}
}

// isLoopback reports whether addr binds only to the loopback interface.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

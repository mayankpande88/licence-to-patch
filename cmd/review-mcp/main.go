// Command review-mcp exposes the "post a PR review" action as an MCP server over
// streamable HTTP.
//
// The single tool, post_pr_review, is intentionally UN-gated: posting a review is
// advisory feedback, not an irreversible change, so it should not stop to ask. The
// human-approval gate lives on the action that actually rewrites the code — see
// fix-mcp's revert_bump_on_pr.
//
// The GitHub token is read from GITHUB_TOKEN and stays in this process; it never
// reaches the model or the sandbox.
//
// Usage:
//
//	GITHUB_TOKEN=... review-mcp [-addr 127.0.0.1:8972]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/mayankpande88/licence-to-patch/internal/ghreview"
)

func main() {
	// Bind to loopback by default: this exposes a DESTRUCTIVE action and the only
	// gate is the harness, which runs locally. Anything reachable on the port
	// could post reviews with GITHUB_TOKEN, so do not expose it on a shared host
	// without adding real authentication in front.
	addr := flag.String("addr", "127.0.0.1:8972", "listen address for the streamable-HTTP MCP server")
	allowRemote := flag.Bool("allow-remote", false, "permit binding to a non-loopback address (unauthenticated — do not use without an auth proxy)")
	flag.Parse()

	if !*allowRemote && !isLoopback(*addr) {
		log.Fatalf("refusing to bind %q: this server has no authentication and exposes a destructive action; "+
			"keep it on loopback, or pass -allow-remote only behind an auth proxy", *addr)
	}

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		log.Fatal("GITHUB_TOKEN is required")
	}
	client := ghreview.New(token)

	s := server.NewMCPServer(
		"licence-to-patch-review",
		"0.1.0",
		server.WithToolCapabilities(true),
	)

	tool := mcp.NewTool(
		"post_pr_review",
		mcp.WithDescription(
			"Post a review on a GitHub pull request: leave the trust brief as a comment, or "+
				"REQUEST_CHANGES to flag a PR that contains an unsafe bump, or APPROVE a clean one. "+
				"This is advisory feedback and runs without approval; the human gate is on the "+
				"code-changing fix (see the fix server), not on posting a review.",
		),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Repository owner, e.g. mayankpande88")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Repository name, e.g. azmetrics-demo")),
		mcp.WithNumber("pull_number", mcp.Required(), mcp.Description("Pull request number")),
		mcp.WithString("event", mcp.Required(),
			mcp.Description("One of COMMENT, REQUEST_CHANGES, APPROVE"),
			mcp.Enum("COMMENT", "REQUEST_CHANGES", "APPROVE")),
		mcp.WithString("body", mcp.Required(), mcp.Description("Markdown body of the review (the trust brief)")),
		// Intentionally left un-annotated so the harness does NOT gate it: posting a
		// review is advisory feedback, not an irreversible change. The gate belongs on
		// the code-changing action (see fix-mcp's revert_bump_on_pr), not on a comment.
	)

	s.AddTool(tool, handler(client))

	log.Printf("review MCP server (streamable HTTP) listening on %s (endpoint /mcp)", *addr)
	httpServer := server.NewStreamableHTTPServer(s)
	if err := httpServer.Start(*addr); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// isLoopback reports whether addr binds only to the loopback interface. An empty
// host (e.g. ":8972") binds to all interfaces and is treated as non-loopback.
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

func handler(client *ghreview.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		owner, err := req.RequireString("owner")
		if err != nil {
			return mcp.NewToolResultErrorf("invalid 'owner': %v", err), nil
		}
		repo, err := req.RequireString("repo")
		if err != nil {
			return mcp.NewToolResultErrorf("invalid 'repo': %v", err), nil
		}
		number, err := pullNumber(req)
		if err != nil {
			return mcp.NewToolResultErrorf("invalid 'pull_number': %v", err), nil
		}
		event, err := req.RequireString("event")
		if err != nil {
			return mcp.NewToolResultErrorf("invalid 'event': %v", err), nil
		}
		body, err := req.RequireString("body")
		if err != nil {
			return mcp.NewToolResultErrorf("invalid 'body': %v", err), nil
		}

		res, err := client.PostReview(ctx, owner, repo, number, ghreview.Event(event), body)
		if err != nil {
			return mcp.NewToolResultErrorf("post review failed: %v", err), nil
		}
		out, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return mcp.NewToolResultErrorf("marshal failed: %v", err), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	}
}

// pullNumber reads pull_number as a strict positive integer. JSON numbers arrive
// as float64, so RequireInt would silently truncate 7.9 -> 7 and review the
// wrong PR; reject any non-integral or non-positive value instead.
func pullNumber(req mcp.CallToolRequest) (int, error) {
	raw, ok := req.GetArguments()["pull_number"]
	if !ok {
		return 0, fmt.Errorf("missing")
	}
	f, ok := raw.(float64)
	if !ok {
		return 0, fmt.Errorf("must be a number")
	}
	if f != math.Trunc(f) || f <= 0 {
		return 0, fmt.Errorf("must be a positive whole number, got %v", f)
	}
	return int(f), nil
}

// Package fix applies the one irreversible action in the workflow: it changes a
// pull request's code. It reverts a single dependency bump on the PR branch —
// pinning the module back to a safe version and tidying — then commits and
// pushes. Posting a review is advice; this rewrites the branch, so it is the
// step that must sit behind a human approval gate.
package fix

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// Fixer reverts a dependency bump on a branch using a GitHub token.
type Fixer struct {
	Token string
	// Committer identity for the revert commit.
	AuthorName  string
	AuthorEmail string
}

// New returns a Fixer with a default bot identity.
func New(token string) *Fixer {
	return &Fixer{
		Token:       token,
		AuthorName:  "licence-to-patch",
		AuthorEmail: "licence-to-patch@users.noreply.github.com",
	}
}

// Result describes the pushed revert.
type Result struct {
	Branch    string `json:"branch"`
	Module    string `json:"module"`
	PinnedTo  string `json:"pinned_to"`
	Commit    string `json:"commit"`
	Pushed    bool   `json:"pushed"`
	NoChange  bool   `json:"no_change"`
	GoModDiff string `json:"go_mod_diff"`
}

// RevertBump clones branch, pins module back to safeVersion, tidies, and pushes.
func (f *Fixer) RevertBump(ctx context.Context, owner, repo, branch, module, safeVersion string) (Result, error) {
	if f.Token == "" {
		return Result{}, fmt.Errorf("no GitHub token configured")
	}
	for name, v := range map[string]string{"owner": owner, "repo": repo, "branch": branch, "module": module, "version": safeVersion} {
		if v == "" || !nameRe.MatchString(v) {
			return Result{}, fmt.Errorf("invalid %s %q", name, v)
		}
	}

	dir, err := os.MkdirTemp("", "lp-fix-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)

	cloneURL := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", f.Token, owner, repo)
	if out, err := f.run(ctx, dir, "git", "clone", "--branch", branch, "--depth", "1", cloneURL, "."); err != nil {
		return Result{}, fmt.Errorf("clone %s#%s: %w: %s", repo, branch, err, redact(out, f.Token))
	}

	// Revert just this module to the safe version and tidy.
	env := append(os.Environ(), "GOFLAGS=-mod=mod")
	if out, err := f.runEnv(ctx, dir, env, "go", "get", module+"@"+safeVersion); err != nil {
		return Result{}, fmt.Errorf("go get %s@%s: %w: %s", module, safeVersion, err, out)
	}
	if out, err := f.runEnv(ctx, dir, env, "go", "mod", "tidy"); err != nil {
		return Result{}, fmt.Errorf("go mod tidy: %w: %s", err, out)
	}

	res := Result{Branch: branch, Module: module, PinnedTo: safeVersion}

	// Nothing to commit means the branch already had the safe version.
	if out, _ := f.run(ctx, dir, "git", "status", "--porcelain"); strings.TrimSpace(out) == "" {
		res.NoChange = true
		return res, nil
	}
	res.GoModDiff, _ = f.run(ctx, dir, "git", "diff", "--", "go.mod")

	msg := fmt.Sprintf("revert %s to %s (held by Licence to Patch)\n\nThis bump was flagged as an unsafe contract change; pinning it back while the rest of the group can merge.", module, safeVersion)
	for _, args := range [][]string{
		{"config", "user.name", f.AuthorName},
		{"config", "user.email", f.AuthorEmail},
		{"commit", "-am", msg},
	} {
		if out, err := f.run(ctx, dir, "git", args...); err != nil {
			return res, fmt.Errorf("git %s: %w: %s", args[0], err, out)
		}
	}
	sha, _ := f.run(ctx, dir, "git", "rev-parse", "HEAD")
	res.Commit = strings.TrimSpace(sha)

	if out, err := f.run(ctx, dir, "git", "push", "origin", "HEAD:"+branch); err != nil {
		return res, fmt.Errorf("push: %w: %s", err, redact(out, f.Token))
	}
	res.Pushed = true
	return res, nil
}

func (f *Fixer) run(ctx context.Context, dir, name string, args ...string) (string, error) {
	return f.runEnv(ctx, dir, os.Environ(), name, args...)
}

func (f *Fixer) runEnv(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func redact(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "***")
}

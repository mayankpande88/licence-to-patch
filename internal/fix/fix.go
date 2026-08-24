// Package fix applies the one irreversible action in the workflow: it changes a
// pull request's code. It reverts a single dependency bump on the PR branch —
// pinning the module back to a safe version and tidying — then commits and
// pushes. Posting a review is advice; this rewrites the branch, so it is the
// step that must sit behind a human approval gate.
package fix

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// nameRe validates owner/repo/branch/module path segments; versionRe additionally
// allows '+' so Go forms like v2.0.0+incompatible and +incompatible pseudo-versions
// are accepted.
var (
	nameRe    = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	versionRe = regexp.MustCompile(`^[A-Za-z0-9._+/-]+$`)
)

// Fixer reverts a dependency bump on a branch using a GitHub token.
type Fixer struct {
	Token       string
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
	for name, v := range map[string]string{"owner": owner, "repo": repo, "branch": branch, "module": module} {
		if v == "" || !nameRe.MatchString(v) {
			return Result{}, fmt.Errorf("invalid %s %q", name, v)
		}
	}
	if safeVersion == "" || !versionRe.MatchString(safeVersion) {
		return Result{}, fmt.Errorf("invalid version %q", safeVersion)
	}

	dir, err := os.MkdirTemp("", "lp-fix-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)

	// Authenticate via an http.extraheader in a throwaway git config passed through
	// GIT_CONFIG_GLOBAL — the token lives in a 0600 file and never appears in argv
	// (unlike embedding it in the clone URL, which leaks via ps / /proc/*/cmdline).
	gitEnv, cleanup, err := f.authEnv(dir)
	if err != nil {
		return Result{}, err
	}
	defer cleanup()

	url := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
	if out, err := f.run(ctx, dir, gitEnv, "git", "clone", "--branch", branch, "--depth", "1", url, "."); err != nil {
		return Result{}, fmt.Errorf("clone %s#%s: %w: %s", repo, branch, err, out)
	}

	goEnv := append(gitEnv, "GOFLAGS=-mod=mod")
	if out, err := f.run(ctx, dir, goEnv, "go", "get", module+"@"+safeVersion); err != nil {
		return Result{}, fmt.Errorf("go get %s@%s: %w: %s", module, safeVersion, err, out)
	}
	if out, err := f.run(ctx, dir, goEnv, "go", "mod", "tidy"); err != nil {
		return Result{}, fmt.Errorf("go mod tidy: %w: %s", err, out)
	}

	res := Result{Branch: branch, Module: module, PinnedTo: safeVersion}
	if out, _ := f.run(ctx, dir, gitEnv, "git", "status", "--porcelain"); strings.TrimSpace(out) == "" {
		res.NoChange = true
		return res, nil
	}
	res.GoModDiff, _ = f.run(ctx, dir, gitEnv, "git", "diff", "--", "go.mod")

	msg := fmt.Sprintf("revert %s to %s (held by Licence to Patch)\n\nThis bump was flagged as an unsafe contract change; pinning it back while the rest of the group can merge.", module, safeVersion)
	steps := [][]string{
		{"config", "user.name", f.AuthorName},
		{"config", "user.email", f.AuthorEmail},
		{"add", "-A"}, // stage new files too (e.g. a freshly generated go.sum)
		{"commit", "-m", msg},
	}
	for _, args := range steps {
		if out, err := f.run(ctx, dir, gitEnv, "git", args...); err != nil {
			return res, fmt.Errorf("git %s: %w: %s", args[0], err, out)
		}
	}
	sha, _ := f.run(ctx, dir, gitEnv, "git", "rev-parse", "HEAD")
	res.Commit = strings.TrimSpace(sha)

	if out, err := f.run(ctx, dir, gitEnv, "git", "push", "origin", "HEAD:"+branch); err != nil {
		return res, fmt.Errorf("push: %w: %s", err, out)
	}
	res.Pushed = true
	return res, nil
}

// authEnv writes a throwaway git config that adds an Authorization header for
// github.com, and returns an env slice pointing GIT_CONFIG_GLOBAL at it.
func (f *Fixer) authEnv(dir string) ([]string, func(), error) {
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + f.Token))
	cfg := filepath.Join(dir, ".gitconfig")
	content := fmt.Sprintf("[http \"https://github.com/\"]\n\textraheader = Authorization: Basic %s\n", basic)
	if err := os.WriteFile(cfg, []byte(content), 0o600); err != nil {
		return nil, func() {}, err
	}
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL="+cfg, "GIT_CONFIG_SYSTEM=/dev/null", "GIT_TERMINAL_PROMPT=0")
	return env, func() { os.Remove(cfg) }, nil
}

func (f *Fixer) run(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

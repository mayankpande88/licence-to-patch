// Package ghreview posts a review to a GitHub pull request.
//
// This is the irreversible, human-gated action of Licence to Patch: after the
// agent has assembled its per-bump trust brief, it asks a person to approve
// leaving a review on the real PR. The GitHub token is read from the
// environment and never travels through the agent, the model, or the sandbox.
package ghreview

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Event is a GitHub pull-request review event.
type Event string

const (
	Comment        Event = "COMMENT"
	RequestChanges Event = "REQUEST_CHANGES"
	Approve        Event = "APPROVE"
)

// Valid reports whether e is a recognised review event.
func (e Event) Valid() bool {
	switch e {
	case Comment, RequestChanges, Approve:
		return true
	default:
		return false
	}
}

// Client posts reviews using a GitHub token.
type Client struct {
	Token   string
	BaseURL string // defaults to https://api.github.com
	HTTP    *http.Client
}

// New returns a Client with sensible defaults.
func New(token string) *Client {
	return &Client{
		Token:   token,
		BaseURL: "https://api.github.com",
		HTTP:    &http.Client{Timeout: 20 * time.Second},
	}
}

// Result identifies the posted review.
type Result struct {
	ReviewID int64  `json:"review_id"`
	State    string `json:"state"`
	HTMLURL  string `json:"html_url"`
}

// PostReview submits a review on owner/repo#number with the given event and body.
func (c *Client) PostReview(ctx context.Context, owner, repo string, number int, event Event, body string) (Result, error) {
	if c.Token == "" {
		return Result{}, fmt.Errorf("no GitHub token configured")
	}
	if !event.Valid() {
		return Result{}, fmt.Errorf("invalid review event %q", event)
	}

	payload, err := json.Marshal(map[string]any{"event": string(event), "body": body})
	if err != nil {
		return Result{}, err
	}

	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/reviews", c.BaseURL, owner, repo, number)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("posting review: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return Result{}, fmt.Errorf("github returned %s: %s", resp.Status, bytes.TrimSpace(raw))
	}

	var out struct {
		ID      int64  `json:"id"`
		State   string `json:"state"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Result{}, fmt.Errorf("decoding review response: %w", err)
	}
	return Result{ReviewID: out.ID, State: out.State, HTMLURL: out.HTMLURL}, nil
}

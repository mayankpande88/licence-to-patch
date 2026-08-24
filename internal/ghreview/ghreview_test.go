package ghreview

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostReview_ShapesRequestAndParsesResponse(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":42,"state":"CHANGES_REQUESTED","html_url":"https://example/r/42"}`)
	}))
	defer srv.Close()

	c := New("tok")
	c.BaseURL = srv.URL

	res, err := c.PostReview(context.Background(), "own", "rep", 7, RequestChanges, "hold armmonitor")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/repos/own/rep/pulls/7/reviews" {
		t.Errorf("path = %s", gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("auth = %s", gotAuth)
	}
	if gotBody["event"] != "REQUEST_CHANGES" || gotBody["body"] != "hold armmonitor" {
		t.Errorf("body = %v", gotBody)
	}
	if res.ReviewID != 42 || res.State != "CHANGES_REQUESTED" {
		t.Errorf("result = %+v", res)
	}
}

func TestPostReview_RejectsInvalidEvent(t *testing.T) {
	c := New("tok")
	if _, err := c.PostReview(context.Background(), "o", "r", 1, Event("NUKE"), "x"); err == nil {
		t.Fatal("expected error for invalid event")
	}
}

func TestPostReview_RequiresToken(t *testing.T) {
	c := New("")
	if _, err := c.PostReview(context.Background(), "o", "r", 1, Comment, "x"); err == nil {
		t.Fatal("expected error when token is empty")
	}
}

func TestPostReview_RejectsMalformedOwnerRepo(t *testing.T) {
	c := New("tok")
	for _, tc := range []struct{ owner, repo string }{
		{"own/../etc", "repo"},
		{"owner", "repo/pulls/9/reviews"},
		{"has space", "repo"},
	} {
		if _, err := c.PostReview(context.Background(), tc.owner, tc.repo, 1, Comment, "x"); err == nil {
			t.Errorf("expected rejection for owner=%q repo=%q", tc.owner, tc.repo)
		}
	}
}

func TestPostComment_ShapesRequest(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":7,"html_url":"https://example/c/7"}`)
	}))
	defer srv.Close()

	c := New("tok")
	c.BaseURL = srv.URL
	res, err := c.PostComment(context.Background(), "own", "rep", 3, "@dependabot recreate")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/repos/own/rep/issues/3/comments" {
		t.Errorf("path = %s", gotPath)
	}
	if gotBody["body"] != "@dependabot recreate" {
		t.Errorf("body = %v", gotBody)
	}
	if res.ReviewID != 7 {
		t.Errorf("result = %+v", res)
	}
}

func TestPostComment_RejectsMalformed(t *testing.T) {
	c := New("tok")
	if _, err := c.PostComment(context.Background(), "own/../x", "rep", 1, "x"); err == nil {
		t.Fatal("expected rejection for malformed owner")
	}
}

func TestPostReview_SurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"message":"Unprocessable"}`)
	}))
	defer srv.Close()

	c := New("tok")
	c.BaseURL = srv.URL
	_, err := c.PostReview(context.Background(), "o", "r", 1, Comment, "x")
	if err == nil || !strings.Contains(err.Error(), "422") {
		t.Fatalf("expected a 422 error, got %v", err)
	}
}

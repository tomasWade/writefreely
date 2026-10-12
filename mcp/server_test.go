/*
 * Copyright © 2026 tomasWade.
 *
 * This file is part of WriteFreely-MCP.
 *
 * WriteFreely-MCP is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License, included
 * in the LICENSE file in this source code package.
 */

package mcp

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

const fakeToken = "11111111-2222-3333-4444-555555555555"
const fakeUserID = int64(7)

// fakeStore is an in-memory Store that records calls for assertions.
type fakeStore struct {
	gotUserID  int64
	posts      map[string]PostRef
	nextID     int
	createErr  error
	updateGot  map[string]*string // postID -> pointer copy received
	createCalls int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		posts: map[string]PostRef{
			"existing01": {
				ID: "existing01", Slug: "hello", Title: "Hello",
				Content: "# Hello\n\nWorld", CollectionAlias: "blog",
				URL:     "http://test.host/blog/hello",
				Created: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				Updated: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
			},
		},
		updateGot: map[string]*string{},
	}
}

func (f *fakeStore) GetUserID(accessToken string) int64 {
	if accessToken == fakeToken {
		return fakeUserID
	}
	return -1
}

func (f *fakeStore) CreatePost(userID int64, title, content, collAlias string) (PostRef, error) {
	f.gotUserID = userID
	f.createCalls++
	if f.createErr != nil {
		return PostRef{}, f.createErr
	}
	f.nextID++
	id := fmt.Sprintf("post%08d", f.nextID)
	ref := PostRef{
		ID: id, Title: title, Content: content, CollectionAlias: collAlias,
		URL:     "http://test.host/" + collAlias + "/" + id,
		Created: time.Now(), Updated: time.Now(),
	}
	f.posts[id] = ref
	return ref, nil
}

func (f *fakeStore) UpdatePost(userID int64, postID string, title, content *string) error {
	f.gotUserID = userID
	f.updateGot[postID+":title"] = title
	f.updateGot[postID+":content"] = content
	return nil
}

func (f *fakeStore) DeletePost(userID int64, postID string) error {
	f.gotUserID = userID
	if _, ok := f.posts[postID]; !ok {
		return fmt.Errorf("post not found or not owned")
	}
	delete(f.posts, postID)
	return nil
}

func (f *fakeStore) GetPost(userID int64, postID string) (PostRef, error) {
	f.gotUserID = userID
	p, ok := f.posts[postID]
	if !ok {
		return PostRef{}, fmt.Errorf("post not found or not owned")
	}
	return p, nil
}

func (f *fakeStore) ListPosts(userID int64, limit int, includeContent bool) ([]PostRef, error) {
	f.gotUserID = userID
	refs := make([]PostRef, 0, len(f.posts))
	for _, p := range f.posts {
		if !includeContent {
			p.Content = ""
		}
		refs = append(refs, p)
	}
	if limit > 0 && len(refs) > limit {
		refs = refs[:limit]
	}
	return refs, nil
}

func (f *fakeStore) ListCollections(userID int64) ([]CollRef, error) {
	f.gotUserID = userID
	return []CollRef{{Alias: "blog", Title: "Blog", URL: "http://test.host/blog/"}}, nil
}

// newTestServer spins up the full handler over a fake store.
func newTestServer(t *testing.T) (*httptest.Server, *fakeStore) {
	t.Helper()
	store := newFakeStore()
	srv := httptest.NewServer(NewHandler(store))
	t.Cleanup(srv.Close)
	return srv, store
}

// newClient returns an initialized MCP client talking to the test server
// with the given Authorization header value.
func newClient(t *testing.T, srv *httptest.Server, auth string) *client.Client {
	t.Helper()
	tr, err := transport.NewStreamableHTTP(srv.URL, transport.WithHTTPHeaders(map[string]string{
		"Authorization": auth,
	}))
	if err != nil {
		t.Fatalf("NewStreamableHTTP: %v", err)
	}
	c := client.NewClient(tr)
	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "mcp-test", Version: "0"}
	initReq.Params.Capabilities = mcp.ClientCapabilities{}
	if _, err := c.Initialize(t.Context(), initReq); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return c
}

func callTool(t *testing.T, c *client.Client, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := c.CallTool(t.Context(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: name, Arguments: args},
	})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return res
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res.IsError {
		t.Fatalf("tool returned error result: %+v", res.Content)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func TestAuthentication(t *testing.T) {
	srv, _ := newTestServer(t)

	cases := []struct {
		name   string
		auth   string
		wantOK bool
	}{
		{"no header", "", false},
		{"bad token", "Bearer nope-nope-nope", false},
		{"raw token without bearer prefix", fakeToken, true},
		{"bearer token", "Bearer " + fakeToken, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.wantOK {
				c := newClient(t, srv, tc.auth)
				res := callTool(t, c, "list_my_collections", nil)
				if !strings.Contains(resultText(t, res), "blog") {
					t.Fatalf("expected collection listing, got: %s", resultText(t, res))
				}
			} else {
				// The endpoint must reject at the HTTP layer before any
				// JSON-RPC processing.
				req, _ := http.NewRequest("POST", srv.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "application/json, text/event-stream")
				if tc.auth != "" {
					req.Header.Set("Authorization", tc.auth)
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatalf("request: %v", err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusUnauthorized {
					t.Fatalf("want 401, got %d", resp.StatusCode)
				}
			}
		})
	}
}

func TestToolsList(t *testing.T) {
	srv, _ := newTestServer(t)
	c := newClient(t, srv, "Bearer "+fakeToken)

	res, err := c.ListTools(t.Context(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	want := map[string]bool{
		"publish_post": false, "edit_post": false, "delete_post": false,
		"read_post": false, "list_my_posts": false, "list_my_collections": false,
	}
	for _, tool := range res.Tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("tool %s not advertised", name)
		}
	}
}

func TestPublishPost(t *testing.T) {
	srv, store := newTestServer(t)
	c := newClient(t, srv, "Bearer "+fakeToken)

	res := callTool(t, c, "publish_post", map[string]any{
		"title": "Test Post", "content": "# Body\n\nSome markdown", "collection": "blog",
	})
	text := resultText(t, res)
	for _, want := range []string{"id: post", "title: Test Post", "collection: blog", "url: http://test.host/blog/post"} {
		if !strings.Contains(text, want) {
			t.Errorf("result missing %q:\n%s", want, text)
		}
	}
	if store.gotUserID != fakeUserID {
		t.Errorf("store got userID %d, want %d", store.gotUserID, fakeUserID)
	}

	// Empty content must be rejected at the tool layer.
	errRes := callTool(t, c, "publish_post", map[string]any{"content": "   "})
	if !errRes.IsError {
		t.Errorf("empty content should be an error result")
	}
}

func TestEditPostNilSemantics(t *testing.T) {
	srv, store := newTestServer(t)
	c := newClient(t, srv, "Bearer "+fakeToken)

	// Only pass title: content pointer must stay nil (field untouched).
	callTool(t, c, "edit_post", map[string]any{"post_id": "existing01", "title": "New Title"})
	if got := store.updateGot["existing01:title"]; got == nil || *got != "New Title" {
		t.Errorf("title pointer = %v, want New Title", got)
	}
	if got := store.updateGot["existing01:content"]; got != nil {
		t.Errorf("content pointer = %v, want nil", got)
	}

	// Passing an empty title must clear it (non-nil empty string).
	callTool(t, c, "edit_post", map[string]any{"post_id": "existing01", "title": ""})
	if got := store.updateGot["existing01:title"]; got == nil || *got != "" {
		t.Errorf("cleared title pointer = %v, want non-nil empty string", got)
	}

	// Passing nothing at all must be rejected client-side.
	errRes := callTool(t, c, "edit_post", map[string]any{"post_id": "existing01"})
	if !errRes.IsError {
		t.Errorf("edit with no fields should be an error result")
	}
}

func TestReadAndListAndDelete(t *testing.T) {
	srv, _ := newTestServer(t)
	c := newClient(t, srv, "Bearer "+fakeToken)

	res := callTool(t, c, "read_post", map[string]any{"post_id": "existing01"})
	text := resultText(t, res)
	if !strings.Contains(text, "# Hello") {
		t.Errorf("read_post missing original markdown:\n%s", text)
	}

	res = callTool(t, c, "list_my_posts", map[string]any{"include_content": false})
	text = resultText(t, res)
	if strings.Contains(text, "# Hello") {
		t.Errorf("list without include_content leaked content:\n%s", text)
	}

	res = callTool(t, c, "delete_post", map[string]any{"post_id": "existing01"})
	if !strings.Contains(resultText(t, res), "existing01 deleted") {
		t.Fatalf("unexpected delete result: %s", resultText(t, res))
	}

	errRes := callTool(t, c, "read_post", map[string]any{"post_id": "existing01"})
	if !errRes.IsError {
		t.Errorf("reading deleted post should error")
	}
}

func TestBearerTokenParsing(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"Bearer abc", "abc"},
		{"bearer abc", "abc"},
		{"BEARER abc", "abc"},
		{"abc", "abc"},
		{"  Bearer   abc  ", "abc"},
	}
	for _, tc := range cases {
		if got := bearerToken(tc.in); got != tc.want {
			t.Errorf("bearerToken(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestStatelessProtocolSmoke ensures the endpoint answers a bare JSON-RPC
// tools/list request — no initialize, no session — which is the minimal
// shape a stateless-protocol client may send.
func TestStatelessProtocolSmoke(t *testing.T) {
	srv, _ := newTestServer(t)

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	req, _ := http.NewRequest("POST", srv.URL, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+fakeToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "publish_post") {
		t.Errorf("tools listing missing publish_post: %.200s", string(b))
	}
}

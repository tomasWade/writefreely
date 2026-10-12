//go:build sqlite && !wflib

/*
 * Copyright © 2026 tomasWade.
 *
 * This file is part of WriteFreely-MCP.
 *
 * WriteFreely-MCP is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License, included
 * in the LICENSE file in this source code package.
 */

package writefreely

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/writefreely/writefreely/config"
	"github.com/writefreely/writefreely/migrations"
	wfmcp "github.com/writefreely/writefreely/mcp"
)

// newSQLiteDatastore creates an in-memory SQLite datastore with the full
// schema and migrations applied, mirroring adminInitDatabase.
func newSQLiteDatastore(t *testing.T) *datastore {
	t.Helper()
	db, err := sql.Open("sqlite3_with_regex", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ds := &datastore{DB: db, driverName: driverSQLite}

	for _, q := range strings.Split(sqliteSql, ";\n") {
		if strings.TrimSpace(q) == "" {
			continue
		}
		if _, err := ds.Exec(q); err != nil {
			t.Fatalf("exec schema statement: %v\nstatement: %s", err, q)
		}
	}
	if err := migrations.SetInitialMigrations(migrations.NewDatastore(ds.DB, ds.driverName)); err != nil {
		t.Fatalf("set initial migrations: %v", err)
	}
	if err := migrations.Migrate(migrations.NewDatastore(ds.DB, ds.driverName)); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return ds
}

func (h *mcpTestHarness) createUser(username string) (int64, string) {
	h.t.Helper()
	u := &User{Username: username, HashedPass: []byte("test-hash")}
	if err := h.ds.CreateUser(h.cfg, u, "", ""); err != nil {
		h.t.Fatalf("CreateUser(%s): %v", username, err)
	}
	token, err := h.ds.GetTemporaryAccessToken(u.ID, 0)
	if err != nil {
		h.t.Fatalf("GetTemporaryAccessToken(%s): %v", username, err)
	}
	return u.ID, token
}

type mcpTestHarness struct {
	t      *testing.T
	ds     *datastore
	cfg    *config.Config
	srv    *httptest.Server
	token  string
	userID int64
}

func newMCPServerHarness(t *testing.T) *mcpTestHarness {
	t.Helper()
	ds := newSQLiteDatastore(t)
	cfg := config.New()
	cfg.App.Host = "http://test.host"
	cfg.App.Federation = false // matches deploy config; the App below is minimal
	cfg.App.MaxBlogs = 2       // the collection-filter test needs a second blog
	cfg.Database.Type = driverSQLite

	h := &mcpTestHarness{t: t, ds: ds, cfg: cfg}
	h.srv = httptest.NewServer(wfmcp.NewHandler(NewMCPStoreFromApp(&App{db: ds, cfg: cfg})))
	t.Cleanup(h.srv.Close)

	h.userID, h.token = h.createUser("opencode")
	return h
}

func (h *mcpTestHarness) clientFor(token string) *client.Client {
	h.t.Helper()
	tr, err := transport.NewStreamableHTTP(h.srv.URL, transport.WithHTTPHeaders(map[string]string{
		"Authorization": "Bearer " + token,
	}))
	if err != nil {
		h.t.Fatalf("transport: %v", err)
	}
	c := client.NewClient(tr)
	if err := c.Start(context.Background()); err != nil {
		h.t.Fatalf("start: %v", err)
	}
	h.t.Cleanup(func() { c.Close() })

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "mcp-itest", Version: "0"}
	initReq.Params.Capabilities = mcp.ClientCapabilities{}
	if _, err := c.Initialize(context.Background(), initReq); err != nil {
		h.t.Fatalf("initialize: %v", err)
	}
	return c
}

func (h *mcpTestHarness) call(c *client.Client, name string, args map[string]any) *mcp.CallToolResult {
	h.t.Helper()
	res, err := c.CallTool(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: name, Arguments: args},
	})
	if err != nil {
		h.t.Fatalf("CallTool(%s): %v", name, err)
	}
	return res
}

func (h *mcpTestHarness) text(res *mcp.CallToolResult) string {
	h.t.Helper()
	if res.IsError {
		h.t.Fatalf("unexpected tool error: %+v", res.Content)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// TestMCPIntegrationLifecycle walks publish → list → read → edit → delete
// over real SQLite through the Streamable HTTP protocol.
func TestMCPIntegrationLifecycle(t *testing.T) {
	h := newMCPServerHarness(t)
	c := h.clientFor(h.token)

	// Publish.
	res := h.call(c, "publish_post", map[string]any{
		"title":   "First Post",
		"content": "# First\n\nHello **world**",
	})
	text := h.text(res)
	if !strings.Contains(text, "collection: opencode") {
		t.Errorf("default collection not used:\n%s", text)
	}
	if !strings.Contains(text, "url: http://test.host/opencode/first-post") {
		t.Errorf("unexpected URL:\n%s", text)
	}
	postID := field(text, "id")
	if postID == "" {
		t.Fatalf("no post id in:\n%s", text)
	}

	// A second post so list ordering is observable.
	h.call(c, "publish_post", map[string]any{"content": "Second body"})

	// List: newest first, no content leak by default.
	text = h.text(h.call(c, "list_my_posts", map[string]any{"limit": 10}))
	if strings.Contains(text, "Hello **world**") || strings.Contains(text, "Second body") {
		t.Errorf("list leaked content:\n%s", text)
	}
	firstID := field(text, "id")
	if firstID == postID {
		t.Errorf("list not newest-first:\n%s", text)
	}

	// Read returns original markdown.
	text = h.text(h.call(c, "read_post", map[string]any{"post_id": postID}))
	if !strings.Contains(text, "Hello **world**") {
		t.Errorf("read_post lost markdown:\n%s", text)
	}

	// Edit only the title; content survives.
	h.call(c, "edit_post", map[string]any{"post_id": postID, "title": "Renamed"})
	text = h.text(h.call(c, "read_post", map[string]any{"post_id": postID}))
	if !strings.Contains(text, "title: Renamed") {
		t.Errorf("edit did not rename:\n%s", text)
	}
	if !strings.Contains(text, "Hello **world**") {
		t.Errorf("edit clobbered content:\n%s", text)
	}

	// Collections listing.
	text = h.text(h.call(c, "list_my_collections", nil))
	if !strings.Contains(text, "alias: opencode") {
		t.Errorf("collections listing wrong:\n%s", text)
	}

	// Delete, then confirm reads fail.
	h.call(c, "delete_post", map[string]any{"post_id": postID})
	if res := h.call(c, "read_post", map[string]any{"post_id": postID}); !res.IsError {
		t.Errorf("reading deleted post should error")
	}
}

// TestMCPIntegrationCrossUser ensures ownership isolation: user B can
// neither read nor delete user A's posts.
func TestMCPIntegrationCrossUser(t *testing.T) {
	h := newMCPServerHarness(t)
	a := h.clientFor(h.token)

	text := h.text(h.call(a, "publish_post", map[string]any{"content": "A's secret draft"}))
	postID := field(text, "id")

	_, bToken := h.createUser("guoguo")
	b := h.clientFor(bToken)

	if res := h.call(b, "read_post", map[string]any{"post_id": postID}); !res.IsError {
		t.Errorf("user B read user A's post without error")
	}
	if res := h.call(b, "delete_post", map[string]any{"post_id": postID}); !res.IsError {
		t.Errorf("user B deleted user A's post without error")
	}
	if res := h.call(b, "edit_post", map[string]any{"post_id": postID, "title": "hijack"}); !res.IsError {
		t.Errorf("user B edited user A's post without error")
	}

	// A still owns an intact post.
	text = h.text(h.call(a, "read_post", map[string]any{"post_id": postID}))
	if !strings.Contains(text, "A's secret draft") {
		t.Errorf("post damaged by cross-user attempts:\n%s", text)
	}
}

// TestMCPIntegrationBadToken checks HTTP-level auth rejection through the
// real datastore: a token that is not in accesstokens must yield 401.
func TestMCPIntegrationBadToken(t *testing.T) {
	h := newMCPServerHarness(t)

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	req, _ := http.NewRequest("POST", h.srv.URL, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer 99999999-9999-9999-9999-999999999999")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 for unknown token, got %d", resp.StatusCode)
	}
}

// TestMCPIntegrationCollectionFilterBeforeLimit guards the list_my_posts
// collection filter: filtering must happen before the limit is applied, so
// asking for a quiet collection still returns its posts even when newer
// posts exist elsewhere.
func TestMCPIntegrationCollectionFilterBeforeLimit(t *testing.T) {
	h := newMCPServerHarness(t)
	c := h.clientFor(h.token)

	// A quiet second collection with one post...
	if _, err := h.ds.CreateCollection(h.cfg, "notes", "", h.userID); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	h.call(c, "publish_post", map[string]any{"content": "quiet note", "collection": "notes"})

	// ...buried under several newer posts in the primary collection.
	for i := 0; i < 3; i++ {
		h.call(c, "publish_post", map[string]any{"content": "main stream post"})
	}

	res := h.call(c, "list_my_posts", map[string]any{"collection": "notes"})
	text := h.text(res)
	if !strings.Contains(text, "collection: notes") || !strings.Contains(text, "quiet-note") {
		t.Fatalf("collection filter applied after limit; got:\n%s", text)
	}
	if strings.Contains(text, "main-stream") {
		t.Fatalf("collection filter leaked other collections:\n%s", text)
	}
}

// TestGetPostsFullVsListing pins the data-layer contract: GetPosts keeps
// full content for API/export/ActivityPub consumers, while
// GetPostsForListing truncates at the <!--more--> marker and sets
// HTMLExcerpt so templates link to the full post.
func TestGetPostsFullVsListing(t *testing.T) {
	h := newMCPServerHarness(t)

	full := "intro paragraph\n\n<!--more-->\n\nthe rest of the story"
	if _, err := h.ds.CreatePost(h.userID, h.coll(t).ID, &SubmittedPost{
		Title: &[]string{"marked"}[0], Content: &full,
	}); err != nil {
		t.Fatalf("CreatePost: %v", err)
	}

	time.Sleep(1100 * time.Millisecond) // created vs now() string compare is second-granular

	posts, err := h.ds.GetPosts(h.cfg, h.coll(t), 0, false, false, false, "")
	if err != nil {
		t.Fatalf("GetPosts: %v", err)
	}
	if len(*posts) != 1 || !strings.Contains((*posts)[0].Content, "the rest of the story") {
		t.Fatalf("GetPosts must return full content, got %d posts", len(*posts))
	}

	listing, err := h.ds.GetPostsForListing(h.cfg, h.coll(t), 0, false, false, false, "")
	if err != nil {
		t.Fatalf("GetPostsForListing: %v", err)
	}
	if len(*listing) != 1 {
		t.Fatalf("GetPostsForListing: %d posts", len(*listing))
	}
	lp := (*listing)[0]
	if strings.Contains(lp.Content, "the rest of the story") {
		t.Fatalf("listing variant must truncate at the marker:\n%s", lp.Content)
	}
	if !strings.Contains(lp.Content, "intro paragraph") {
		t.Fatalf("listing variant lost the excerpt:\n%s", lp.Content)
	}
	if lp.HTMLExcerpt == "" {
		t.Fatalf("listing variant must set HTMLExcerpt for the Read more link")
	}
}

func (h *mcpTestHarness) coll(t *testing.T) *Collection {
	h.t.Helper()
	coll, err := h.ds.GetCollection("opencode")
	if err != nil {
		t.Fatalf("GetCollection: %v", err)
	}
	coll.hostName = h.cfg.App.Host
	return coll
}

// field extracts "key: value" from a tool result text body.
func field(text, key string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, key+": ") {
			return strings.TrimPrefix(line, key+": ")
		}
	}
	return ""
}

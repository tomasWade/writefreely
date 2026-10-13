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
	"context"
	"fmt"
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// EndpointPath is the URL path the MCP server is served on.
const EndpointPath = "/api/mcp"

// serverName/protocol semantics reported to clients during initialize.
const (
	serverName    = "writefreely"
	serverVersion = "0.1.0"
)

// NewHandler builds the MCP-over-Streamable-HTTP endpoint. Every request
// must carry a valid WriteFreely access token (Authorization header, with
// or without a "Bearer " prefix); the authenticated user ID is propagated
// to tool handlers via the request context.
func NewHandler(store Store) http.Handler {
	srv := server.NewMCPServer(
		serverName,
		serverVersion,
		server.WithToolCapabilities(false),
	)
	registerTools(srv, store)

	streamable := server.NewStreamableHTTPServer(srv, server.WithStateLess(true))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r.Header.Get("Authorization"))
		if token == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="writefreely-mcp"`)
			http.Error(w, "missing access token", http.StatusUnauthorized)
			return
		}
		userID := store.GetUserID(token)
		if userID == -1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="writefreely-mcp", error="invalid_token"`)
			http.Error(w, "invalid access token", http.StatusUnauthorized)
			return
		}
		streamable.ServeHTTP(w, r.WithContext(WithUserID(r.Context(), userID)))
	})
}

// registerTools wires all MCP tools onto the server. Each handler re-checks
// identity from the context so the tools can never act without it.
func registerTools(srv *server.MCPServer, store Store) {
	srv.AddTool(
		mcp.NewTool("publish_post",
			mcp.WithDescription("Publish a new Markdown post to your WriteFreely blog. "+
				"Returns the post's id, slug and canonical URL. "+
				"The post's visibility follows the collection's settings. "+
				"Posts longer than ~1000 characters automatically get a <!--more--> excerpt marker, "+
				"so listings and the Reader show an excerpt with a link to the full post."),
			mcp.WithString("title", mcp.Description("Post title; may be empty for untitled posts")),
			mcp.WithString("content", mcp.Description("Full post body in Markdown"),
				mcp.Required()),
			mcp.WithString("collection",
				mcp.Description("Alias of the target collection (blog); defaults to your primary collection")),
		),
		handlePublishPost(store),
	)

	srv.AddTool(
		mcp.NewTool("edit_post",
			mcp.WithDescription("Edit an existing post you own. Only the fields you pass are changed; "+
				"pass an empty string to clear the title. "+
				"Note: resubmitting long content re-applies the automatic <!--more--> excerpt marker."),
			mcp.WithString("post_id", mcp.Description("ID of the post to edit"), mcp.Required()),
			mcp.WithString("title", mcp.Description("New title; empty string clears it")),
			mcp.WithString("content", mcp.Description("New full Markdown body")),
		),
		handleEditPost(store),
	)

	srv.AddTool(
		mcp.NewTool("delete_post",
			mcp.WithDescription("Permanently delete a post you own. This cannot be undone."),
			mcp.WithString("post_id", mcp.Description("ID of the post to delete"), mcp.Required()),
		),
		handleDeletePost(store),
	)

	srv.AddTool(
		mcp.NewTool("read_post",
			mcp.WithDescription("Read a post you own, returning the original Markdown content."),
			mcp.WithString("post_id", mcp.Description("ID of the post to read"), mcp.Required()),
		),
		handleReadPost(store),
	)

	srv.AddTool(
		mcp.NewTool("list_my_posts",
			mcp.WithDescription("List your posts, newest first. By default only metadata is returned; "+
				"set include_content to get full Markdown bodies."),
			mcp.WithNumber("limit", mcp.Description("Maximum number of posts to return (default 10, max 100)")),
			mcp.WithBoolean("include_content",
				mcp.Description("Include full Markdown content in the listing (default false)")),
			mcp.WithString("collection",
				mcp.Description("Only list posts from this collection alias")),
		),
		handleListMyPosts(store),
	)

	srv.AddTool(
		mcp.NewTool("list_my_collections",
			mcp.WithDescription("List your collections (blogs) with their aliases and URLs. "+
				"Use an alias as the collection parameter when publishing.")),
		handleListMyCollections(store),
	)
}

// userFrom extracts the user identity for a tool call, producing a
// ready-to-return error result when it is missing.
func userFrom(ctx context.Context) (int64, *mcp.CallToolResult) {
	userID, ok := UserIDFrom(ctx)
	if !ok {
		return 0, mcp.NewToolResultError("unauthenticated: no valid access token")
	}
	return userID, nil
}

// errResult converts any error into an MCP error result.
func errResult(err error) *mcp.CallToolResult {
	return mcp.NewToolResultErrorFromErr("writefreely error", err)
}

// refText renders a PostRef as the human/agent-readable result body.
func refText(p PostRef, includeContent bool) string {
	s := fmt.Sprintf("id: %s\ntitle: %s", p.ID, p.Title)
	if p.Slug != "" {
		s += "\nslug: " + p.Slug
	}
	if p.CollectionAlias != "" {
		s += "\ncollection: " + p.CollectionAlias
	}
	if p.URL != "" {
		s += "\nurl: " + p.URL
	}
	s += fmt.Sprintf("\ncreated: %s\nupdated: %s", p.Created.Format("2006-01-02 15:04:05"), p.Updated.Format("2006-01-02 15:04:05"))
	if includeContent {
		s += "\n\n" + p.Content
	}
	return s
}

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
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultListLimit = 10
	maxListLimit     = 100
)

func handlePublishPost(store Store) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, errRes := userFrom(ctx)
		if errRes != nil {
			return errRes, nil
		}
		content, err := req.RequireString("content")
		if err != nil {
			return mcp.NewToolResultErrorf("invalid parameters: %v", err), nil
		}
		if strings.TrimSpace(content) == "" {
			return mcp.NewToolResultError("invalid parameters: content must not be empty"), nil
		}
		title := req.GetString("title", "")
		collection := req.GetString("collection", "")

		ref, err := store.CreatePost(userID, title, content, collection)
		if err != nil {
			return errResult(err), nil
		}
		return mcp.NewToolResultText(refText(ref, false)), nil
	}
}

func handleEditPost(store Store) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, errRes := userFrom(ctx)
		if errRes != nil {
			return errRes, nil
		}
		postID, err := req.RequireString("post_id")
		if err != nil {
			return mcp.NewToolResultErrorf("invalid parameters: %v", err), nil
		}
		args := req.GetArguments()

		// Distinguish "field absent" (leave unchanged) from "field set to
		// empty string" (clear it) — Title clearing is a legitimate edit.
		var title, content *string
		if v, ok := args["title"].(string); ok {
			title = &v
		}
		if v, ok := args["content"].(string); ok {
			content = &v
		}
		if title == nil && content == nil {
			return mcp.NewToolResultError("invalid parameters: nothing to edit, pass title and/or content"), nil
		}

		if err := store.UpdatePost(userID, postID, title, content); err != nil {
			return errResult(err), nil
		}
		ref, err := store.GetPost(userID, postID)
		if err != nil {
			return mcp.NewToolResultText(fmt.Sprintf("post %s updated", postID)), nil
		}
		return mcp.NewToolResultText("updated\n" + refText(ref, false)), nil
	}
}

func handleDeletePost(store Store) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, errRes := userFrom(ctx)
		if errRes != nil {
			return errRes, nil
		}
		postID, err := req.RequireString("post_id")
		if err != nil {
			return mcp.NewToolResultErrorf("invalid parameters: %v", err), nil
		}
		if err := store.DeletePost(userID, postID); err != nil {
			return errResult(err), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("post %s deleted", postID)), nil
	}
}

func handleReadPost(store Store) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, errRes := userFrom(ctx)
		if errRes != nil {
			return errRes, nil
		}
		postID, err := req.RequireString("post_id")
		if err != nil {
			return mcp.NewToolResultErrorf("invalid parameters: %v", err), nil
		}
		ref, err := store.GetPost(userID, postID)
		if err != nil {
			return errResult(err), nil
		}
		return mcp.NewToolResultText(refText(ref, true)), nil
	}
}

func handleListMyPosts(store Store) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, errRes := userFrom(ctx)
		if errRes != nil {
			return errRes, nil
		}
		limit := defaultListLimit
		if v, ok := req.GetArguments()["limit"].(float64); ok && v >= 1 {
			// Clamp on the float before converting: absurd values would
			// wrap to a negative int and bypass the limit entirely, while
			// 0 < v < 1 would convert to 0 and mean "unbounded".
			if v > float64(maxListLimit) {
				v = float64(maxListLimit)
			}
			limit = int(v)
		}
		includeContent := false
		if v, ok := req.GetArguments()["include_content"].(bool); ok {
			includeContent = v
		}
		collection := req.GetString("collection", "")

		posts, err := store.ListPosts(userID, collection, limit, includeContent)
		if err != nil {
			return errResult(err), nil
		}
		if len(posts) == 0 {
			return mcp.NewToolResultText("no posts"), nil
		}

		var b strings.Builder
		fmt.Fprintf(&b, "%d post(s):\n", len(posts))
		for _, p := range posts {
			b.WriteString("\n---\n")
			b.WriteString(refText(p, includeContent))
		}
		return mcp.NewToolResultText(b.String()), nil
	}
}

func handleListMyCollections(store Store) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		userID, errRes := userFrom(ctx)
		if errRes != nil {
			return errRes, nil
		}
		colls, err := store.ListCollections(userID)
		if err != nil {
			return errResult(err), nil
		}
		if len(colls) == 0 {
			return mcp.NewToolResultText("no collections"), nil
		}

		var b strings.Builder
		fmt.Fprintf(&b, "%d collection(s):\n", len(colls))
		for _, c := range colls {
			b.WriteString("\n---\n")
			fmt.Fprintf(&b, "alias: %s\ntitle: %s", c.Alias, c.Title)
			if c.Description != "" {
				b.WriteString("\ndescription: " + c.Description)
			}
			if c.URL != "" {
				b.WriteString("\nurl: " + c.URL)
			}
		}
		return mcp.NewToolResultText(b.String()), nil
	}
}

/*
 * Copyright © 2026 tomasWade.
 *
 * This file is part of WriteFreely-MCP.
 *
 * WriteFreely-MCP is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License, included
 * in the LICENSE file in this source code package.
 */

// Package mcp exposes WriteFreely post operations as MCP tools over the
// Streamable HTTP transport, so AI agents can publish and manage posts
// without any local per-agent skill.
package mcp

import (
	"context"
	"time"
)

// PostRef is the MCP-facing view of a post. It intentionally mirrors only
// the fields agents need; the writefreely-side adapter fills it in.
type PostRef struct {
	ID              string    `json:"id"`
	Slug            string    `json:"slug,omitempty"`
	Title           string    `json:"title"`
	Content         string    `json:"content,omitempty"`
	CollectionAlias string    `json:"collection,omitempty"`
	URL             string    `json:"url,omitempty"`
	Created         time.Time `json:"created"`
	Updated         time.Time `json:"updated"`
}

// CollRef is the MCP-facing view of a collection (blog).
type CollRef struct {
	Alias       string `json:"alias"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
}

// Store is the persistence surface the MCP tools operate on. The host
// application supplies the implementation; nil user identity means
// "unauthenticated" and is never passed here.
type Store interface {
	// GetUserID resolves a WriteFreely access token to a user ID,
	// returning -1 when the token is invalid or expired.
	GetUserID(accessToken string) int64

	// CreatePost publishes a new post for userID, optionally into the
	// collection with the given alias (empty means the user's default
	// collection).
	CreatePost(userID int64, title, content, collAlias string) (PostRef, error)

	// UpdatePost patches the given fields of a post owned by userID.
	// A nil pointer leaves the field unchanged; a non-nil pointer sets it,
	// possibly to the empty string.
	UpdatePost(userID int64, postID string, title, content *string) error

	// DeletePost removes a post owned by userID. Returns an error when the
	// post doesn't exist or belongs to someone else.
	DeletePost(userID int64, postID string) error

	// GetPost fetches a post owned by userID, including full content.
	GetPost(userID int64, postID string) (PostRef, error)

	// ListPosts returns the userID's posts, newest first, up to limit
	// (limit <= 0 means a sensible default). Content is included only when
	// includeContent is true.
	ListPosts(userID int64, limit int, includeContent bool) ([]PostRef, error)

	// ListCollections returns the collections owned by userID.
	ListCollections(userID int64) ([]CollRef, error)
}

// ctxKey is the private context key type for identity propagation.
type ctxKey int

const (
	userIDKey ctxKey = iota
)

// WithUserID returns a context carrying the authenticated user ID, to be
// consumed by UserIDFrom inside tool handlers.
func WithUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// UserIDFrom extracts the authenticated user ID from a context. It returns
// false when no authenticated user is present.
func UserIDFrom(ctx context.Context) (int64, bool) {
	userID, ok := ctx.Value(userIDKey).(int64)
	return userID, ok && userID > 0
}

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
	"net/http"
	"strings"

	"github.com/writeas/impart"
	"github.com/writeas/web-core/log"
	wfmcp "github.com/writefreely/writefreely/mcp"
)

// wfMCPStore adapts the WriteFreely datastore to the mcp.Store interface.
type wfMCPStore struct {
	db       *datastore
	hostName string
}

// NewMCPStore returns an mcp.Store backed by the given datastore. hostName
// is the configured site host (e.g. http://localhost:18080) used to build
// canonical URLs.
func NewMCPStore(db *datastore, hostName string) wfmcp.Store {
	return &wfMCPStore{db: db, hostName: strings.TrimSuffix(hostName, "/")}
}

// NewMCPStoreFromApp returns an mcp.Store for the given App.
func NewMCPStoreFromApp(app *App) wfmcp.Store {
	return NewMCPStore(app.db, app.cfg.App.Host)
}

func (s *wfMCPStore) GetUserID(accessToken string) int64 {
	return s.db.GetUserID(accessToken)
}

// resolveCollection finds the collection a post should be published into:
// the one named by alias, or the user's primary collection when alias is
// empty (the auto-created one sharing their username, else the first).
func (s *wfMCPStore) resolveCollection(userID int64, alias string) (*Collection, error) {
	u, err := s.db.GetUserByID(userID)
	if err != nil {
		return nil, err
	}
	if alias == "" {
		colls, err := s.db.GetCollections(u, s.hostName)
		if err != nil {
			return nil, err
		}
		for i := range *colls {
			if (*colls)[i].Alias == u.Username {
				return &(*colls)[i], nil
			}
		}
		if len(*colls) > 0 {
			return &(*colls)[0], nil
		}
		return nil, impart.HTTPError{http.StatusNotFound, "You have no collection to publish to."}
	}

	coll, err := s.db.GetCollection(alias)
	if err != nil {
		return nil, err
	}
	if coll.OwnerID != userID {
		return nil, ErrForbiddenCollection
	}
	coll.hostName = s.hostName
	return coll, nil
}

// moreTagThreshold is the content length (in runes) beyond which
// ensureMoreTag inserts a <!--more--> marker, so collection listings and
// the Reader render an excerpt instead of the full body. Full posts stay
// intact on their own page, where WriteFreely strips the marker again.
const moreTagThreshold = 1000

// moreTagSearchWindow bounds how far past the threshold the marker
// insertion searches for a paragraph break before inserting anyway.
const moreTagSearchWindow = 2000

// ensureMoreTag returns content with a <!--more--> excerpt marker if the
// content is long and doesn't already carry one. The marker is placed on a
// paragraph boundary shortly after the threshold so listings stay light.
func ensureMoreTag(content string) string {
	if strings.Contains(content, "<!--more-->") {
		return content
	}
	runes := []rune(content)
	if len(runes) <= moreTagThreshold {
		return content
	}
	for i := moreTagThreshold; i < len(runes) && i < moreTagThreshold+moreTagSearchWindow; i++ {
		if runes[i] == '\n' && i+1 < len(runes) && runes[i+1] == '\n' {
			return string(runes[:i]) + "\n\n<!--more-->\n\n" + string(runes[i+2:])
		}
	}
	// No paragraph break found nearby; insert at the first one anywhere,
	// or fall back to the window end.
	for i := moreTagThreshold + moreTagSearchWindow; i < len(runes); i++ {
		if runes[i] == '\n' && i+1 < len(runes) && runes[i+1] == '\n' {
			return string(runes[:i]) + "\n\n<!--more-->\n\n" + string(runes[i+2:])
		}
	}
	return string(runes[:moreTagThreshold+moreTagSearchWindow]) + "\n\n<!--more-->\n\n" + string(runes[moreTagThreshold+moreTagSearchWindow:])
}

func (s *wfMCPStore) CreatePost(userID int64, title, content, collAlias string) (wfmcp.PostRef, error) {
	silenced, err := s.db.IsUserSilenced(userID)
	if err == nil && silenced {
		return wfmcp.PostRef{}, ErrUserSilenced
	}

	coll, err := s.resolveCollection(userID, collAlias)
	if err != nil {
		return wfmcp.PostRef{}, err
	}

	content = ensureMoreTag(content)
	p, err := s.db.CreatePost(userID, coll.ID, &SubmittedPost{Title: &title, Content: &content})
	if err != nil {
		return wfmcp.PostRef{}, err
	}

	return wfmcp.PostRef{
		ID:              p.ID,
		Slug:            p.Slug.String,
		Title:           p.Title.String,
		CollectionAlias: coll.Alias,
		URL:             coll.CanonicalURL() + p.Slug.String,
		Created:         p.Created,
		Updated:         p.Updated,
	}, nil
}

func (s *wfMCPStore) UpdatePost(userID int64, postID string, title, content *string) error {
	if content != nil {
		c := ensureMoreTag(*content)
		content = &c
	}
	return s.db.UpdateOwnedPost(&AuthenticatedPost{
		ID:            postID,
		SubmittedPost: &SubmittedPost{Title: title, Content: content},
	}, userID)
}

// DeleteOwnedPost deletes a post owned by the given user. It reports
// whether a row was actually removed; false means the post doesn't exist
// or belongs to another user. This is the non-federating counterpart used
// by the MCP tools; the HTTP handler in posts.go keeps its richer logic.
func (db *datastore) DeleteOwnedPost(id string, ownerID int64) (bool, error) {
	res, err := db.Exec("DELETE FROM posts WHERE id = ? AND owner_id = ?", id, ownerID)
	if err != nil {
		log.Error("Unable to delete owned post: %v", err)
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (s *wfMCPStore) DeletePost(userID int64, postID string) error {
	deleted, err := s.db.DeleteOwnedPost(postID, userID)
	if err != nil {
		return err
	}
	if !deleted {
		return impart.HTTPError{http.StatusForbidden, "Post not found, or you're not the owner."}
	}
	return nil
}

// postRef converts a PublicPost into its MCP-facing form, filling in the
// collection details when the post has one.
func (s *wfMCPStore) postRef(pp *PublicPost) wfmcp.PostRef {
	ref := wfmcp.PostRef{
		ID:      pp.ID,
		Slug:    pp.Slug.String,
		Title:   pp.Title.String,
		Content: pp.Content,
		Created: pp.Created,
		Updated: pp.Updated,
	}
	if pp.Collection != nil && pp.Collection.Alias != "" {
		ref.CollectionAlias = pp.Collection.Alias
		ref.URL = s.hostName + "/" + pp.Collection.Alias + "/" + pp.Slug.String
	} else {
		ref.URL = s.hostName + "/" + pp.ID + ".md"
	}
	return ref
}

func (s *wfMCPStore) GetPost(userID int64, postID string) (wfmcp.PostRef, error) {
	pp, err := s.db.GetOwnedPost(postID, userID)
	if err != nil {
		return wfmcp.PostRef{}, err
	}
	if pp.CollectionID.Valid {
		coll, err := s.db.GetCollectionByID(pp.CollectionID.Int64)
		if err == nil && coll.OwnerID == userID {
			pp.Collection = &CollectionObj{Collection: *coll}
		}
	}
	return s.postRef(pp), nil
}

func (s *wfMCPStore) ListPosts(userID int64, limit int, includeContent bool) ([]wfmcp.PostRef, error) {
	u, err := s.db.GetUserByID(userID)
	if err != nil {
		return nil, err
	}
	posts, err := s.db.GetUserPosts(u)
	if err != nil {
		return nil, err
	}

	refs := make([]wfmcp.PostRef, 0, len(*posts))
	// GetUserPosts returns oldest first; MCP consumers want newest first.
	for i := len(*posts) - 1; i >= 0; i-- {
		pp := (*posts)[i]
		if pp.CollectionID.Valid {
			coll, err := s.db.GetCollectionByID(pp.CollectionID.Int64)
			if err == nil {
				pp.Collection = &CollectionObj{Collection: *coll}
			}
		}
		ref := s.postRef(&pp)
		if !includeContent {
			ref.Content = ""
		}
		refs = append(refs, ref)
		if limit > 0 && len(refs) >= limit {
			break
		}
	}
	return refs, nil
}

func (s *wfMCPStore) ListCollections(userID int64) ([]wfmcp.CollRef, error) {
	u, err := s.db.GetUserByID(userID)
	if err != nil {
		return nil, err
	}
	colls, err := s.db.GetCollections(u, s.hostName)
	if err != nil {
		return nil, err
	}
	refs := make([]wfmcp.CollRef, 0, len(*colls))
	for _, c := range *colls {
		refs = append(refs, wfmcp.CollRef{
			Alias:       c.Alias,
			Title:       c.Title,
			Description: c.Description,
			URL:         c.URL,
		})
	}
	return refs, nil
}

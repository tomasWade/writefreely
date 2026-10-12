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
	"strings"
	"testing"
	"unicode/utf8"
)

func repeatParagraph(n int) string {
	p := "这是一段用于测试自动摘要标记的中文段落内容。"
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(p)
		b.WriteString("\n\n")
	}
	return b.String()
}

func TestEnsureMoreTagShortContent(t *testing.T) {
	for _, in := range []string{
		"",
		"short post",
		strings.Repeat("x", moreTagThreshold),
	} {
		if got := ensureMoreTag(in); got != in {
			t.Errorf("ensureMoreTag changed short content (%d runes)", utf8.RuneCountInString(in))
		}
	}
}

func TestEnsureMoreTagRespectsExisting(t *testing.T) {
	in := "intro\n\n<!--more-->\n\n" + repeatParagraph(500)
	if got := ensureMoreTag(in); got != in {
		t.Errorf("ensureMoreTag must not add a second marker")
	}
}

func TestEnsureMoreTagInsertsOnParagraphBoundary(t *testing.T) {
	in := repeatParagraph(200) // well past the threshold
	got := ensureMoreTag(in)
	if strings.Count(got, "<!--more-->") != 1 {
		t.Fatalf("want exactly one marker, got %d", strings.Count(got, "<!--more-->"))
	}
	idx := strings.Index(got, "<!--more-->")
	if utf8.RuneCountInString(got[:idx]) < moreTagThreshold {
		t.Errorf("marker at rune position %d, must be >= threshold %d", utf8.RuneCountInString(got[:idx]), moreTagThreshold)
	}
	// Everything before the marker plus the paragraphs after it must
	// reconstitute the original content minus the inserted marker block.
	before := got[:idx]
	after := got[idx+len("<!--more-->"):]
	if !strings.HasSuffix(before, "\n\n") || !strings.HasPrefix(after, "\n\n") {
		t.Errorf("marker not on a paragraph boundary: ...%q | %q...", before[max(0,idx-4):], after[:min(4, len(after))])
	}
	restored := strings.TrimSuffix(before, "\n\n") + "\n\n" + strings.TrimPrefix(after, "\n\n")
	if restored != in {
		t.Errorf("content mangled around marker")
	}
}

func TestEnsureMoreTagLongUnbrokenContent(t *testing.T) {
	// No paragraph breaks at all: still gets a marker without panicking.
	in := strings.Repeat("字", moreTagThreshold*3)
	got := ensureMoreTag(in)
	if !strings.Contains(got, "<!--more-->") {
		t.Errorf("no marker inserted for unbroken long content")
	}
}

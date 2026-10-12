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

func TestEnsureMoreTagSkipsCodeFences(t *testing.T) {
	// Prose up to just under the threshold, then one huge fenced block
	// covering the whole search window: the marker must land on a prose
	// boundary before the fence, never inside it.
	var b strings.Builder
	for utf8.RuneCountInString(b.String()) < moreTagThreshold-10 {
		b.WriteString("正文段落内容。\n\n")
	}
	prose := b.String()
	in := prose + "```go\n" + strings.Repeat("x\n", moreTagSearchWindow) + "```\n\n尾声段落。\n\n" + repeatParagraph(100)

	got := ensureMoreTag(in)
	idx := strings.Index(got, "<!--more-->")
	if idx == -1 {
		t.Fatalf("no marker inserted")
	}
	// The marker must sit after the fenced block closes, never inside it.
	if lastFence := strings.LastIndex(got, "```"); lastFence > idx {
		t.Errorf("marker at %d inside fenced block (last fence at %d)", idx, lastFence)
	}
	// And the prose above the marker is preserved.
	if !strings.HasPrefix(got, prose[:len(prose)-1]) {
		t.Errorf("prose mangled before marker")
	}
}

func TestEnsureMoreTagBoundaryAfterFencedBlock(t *testing.T) {
	// A fence straddling the threshold: the marker waits for the next
	// prose boundary after the fence closes.
	var b strings.Builder
	for utf8.RuneCountInString(b.String()) < moreTagThreshold-10 {
		b.WriteString("前置正文。\n\n")
	}
	in := b.String() + "```\ncode line\ncode line\n```\n\n围栏之后的段落。\n\n" + repeatParagraph(100)

	got := ensureMoreTag(in)
	idx := strings.Index(got, "<!--more-->")
	if idx == -1 {
		t.Fatalf("no marker inserted")
	}
	// The marker must wait until the fence closes.
	if lastFence := strings.LastIndex(got, "```"); lastFence > idx {
		t.Errorf("marker at %d inside fenced block (last fence at %d)", idx, lastFence)
	}
}

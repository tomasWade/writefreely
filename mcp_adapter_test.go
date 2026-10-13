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
	// No paragraph breaks at all: no safe boundary, so no marker —
	// leaving the post intact beats corrupting it.
	in := strings.Repeat("字", moreTagThreshold*3)
	if got := ensureMoreTag(in); got != in {
		t.Errorf("unbroken content must be left alone, got marker inserted")
	}
}

func TestEnsureMoreTagCRLF(t *testing.T) {
	// CRLF paragraph separators must still yield boundaries (line endings
	// are normalized to LF in the process, which is markdown-equivalent).
	var b strings.Builder
	for i := 0; i < 120; i++ {
		b.WriteString("段落内容测试。\r\n\r\n")
	}
	in := b.String()
	got := ensureMoreTag(in)
	if !strings.Contains(got, "<!--more-->") {
		t.Fatalf("no marker inserted for CRLF content")
	}
	if !strings.Contains(got, "段落内容测试。") {
		t.Errorf("content mangled for CRLF input")
	}
}

func TestEnsureMoreTagFenceDesyncSafety(t *testing.T) {
	// A markdown tutorial: an outer fence whose body contains ```go lines
	// (which must NOT close the outer fence per CommonMark, since closing
	// fences carry no info string). The marker must land on the first safe
	// boundary after the outer fence closes — outside every fence.
	var b strings.Builder
	for utf8.RuneCountInString(b.String()) < moreTagThreshold-10 {
		b.WriteString("教程正文段落。\n\n")
	}
	in := b.String() +
		"```\n内层示例：\n\n```go\nfunc inner() {}\n```\n\n继续内层文字。\n\n" +
		strings.Repeat("外层内容行\n", 3000) +
		"```\n\n结尾段落。\n\n"

	got := ensureMoreTag(in)
	idx := strings.Index(got, "<!--more-->")
	if idx == -1 {
		t.Fatalf("no marker inserted although a safe boundary exists")
	}
	// The marker sits right after the outer fence closes (the first safe
	// boundary past the threshold), with the inner-text paragraph after
	// it — never between the fence's opening and its closing line.
	if !strings.HasSuffix(strings.TrimSpace(got[:idx]), "```") {
		t.Errorf("marker not placed after the closing fence: [%.200s] MARKER", got[max(0,idx-200):idx])
	}
	if !strings.HasPrefix(got[idx:], "<!--more-->\n\n继续内层文字") {
		t.Errorf("inner text not preserved after the marker: [%.100s]", got[idx:idx+100])
	}
	// The inner ```go line must not have closed the outer fence early:
	// the long wall of text after the marker is still there.
	if !strings.Contains(got[idx:], "外层内容行") {
		t.Errorf("content after the marker was lost")
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

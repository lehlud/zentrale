package agent

import (
	"strings"
	"testing"
)

func TestFileShareExcerptKeepsUTF8AndExactContinuation(t *testing.T) {
	text := strings.Repeat("a", defaultShareExcerptBytes-1) + "💬 followed by text"
	// Limit the input just like ReadChunk, which keeps one lookahead byte.
	first, err := textExcerpt([]byte(text)[:defaultShareExcerptBytes+1], 0, defaultShareExcerptBytes)
	if err != nil || len(first.Text) != defaultShareExcerptBytes-1 || first.NextOffset == nil || *first.NextOffset != int64(len(first.Text)) {
		t.Fatalf("first excerpt: len=%d nextOffset=%v err=%v", len(first.Text), first.NextOffset, err)
	}

	second, err := textExcerpt([]byte(text[*first.NextOffset:]), *first.NextOffset, defaultShareExcerptBytes)
	if err != nil || second.NextOffset != nil || first.Text+second.Text != text {
		t.Fatalf("continuation: %+v err=%v", second, err)
	}

	if _, err := textExcerpt([]byte{'a', 0, 'b'}, 0, 3); err == nil {
		t.Fatal("binary bytes were returned as text")
	}
}

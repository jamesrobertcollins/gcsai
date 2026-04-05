package ux

import (
	"testing"

	"github.com/google/generative-ai-go/genai"
)

func TestAINormalizeExternalTextReplacesInvalidUTF8(t *testing.T) {
	raw := string([]byte{'A', 0xff, 'B'})
	if got := aiNormalizeExternalText("test.source", raw); got != "A\ufffdB" {
		t.Fatalf("expected invalid UTF-8 to be normalized, got %q", got)
	}
}

func TestAIMarshalLocalContentNormalizesInvalidUTF8(t *testing.T) {
	content := &genai.Content{Parts: []genai.Part{genai.Text(string([]byte{'A', 0xff, 'B'}))}}
	if got := aiMarshalLocalContent(content); got != "A\ufffdB" {
		t.Fatalf("expected history content to be normalized, got %q", got)
	}
}

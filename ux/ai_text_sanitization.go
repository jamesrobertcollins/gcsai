package ux

import (
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"
)

const aiUTF8Replacement = "\ufffd"

func aiEnsureValidUTF8(text string) string {
	if text == "" || utf8.ValidString(text) {
		return text
	}
	return strings.ToValidUTF8(text, aiUTF8Replacement)
}

func aiNormalizeExternalText(source, text string) string {
	normalized := aiEnsureValidUTF8(text)
	if normalized == text {
		return text
	}
	invalidCount, bytePreview := aiInvalidUTF8Diagnostics(text, 24)
	slog.Warn("normalized invalid UTF-8 text",
		"source", strings.TrimSpace(source),
		"invalid_sequences", invalidCount,
		"byte_preview", bytePreview,
		"text_preview", aiTextPreview(normalized, 80),
	)
	return normalized
}

func aiInvalidUTF8Diagnostics(text string, previewBytes int) (count int, bytePreview string) {
	firstInvalid := -1
	for index := 0; index < len(text); {
		r, size := utf8.DecodeRuneInString(text[index:])
		if r == utf8.RuneError && size == 1 {
			if firstInvalid < 0 {
				firstInvalid = index
			}
			count++
			index++
			continue
		}
		index += size
	}
	if firstInvalid < 0 {
		return 0, ""
	}
	if previewBytes <= 0 {
		previewBytes = 24
	}
	start := max(0, firstInvalid-previewBytes/2)
	end := min(len(text), start+previewBytes)
	return count, fmt.Sprintf("% x", []byte(text[start:end]))
}

func aiTextPreview(text string, maxRunes int) string {
	text = strings.TrimSpace(text)
	if maxRunes <= 0 {
		maxRunes = 80
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes]) + "..."
}

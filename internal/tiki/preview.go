package tiki

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// PreviewRunes bounds the description excerpt carried by ticket snapshots.
const PreviewRunes = 140

// Lists read only this many source runes; full ticket reads use the same bound.
const previewSource = 4 * PreviewRunes

var markdownMarker = regexp.MustCompile(`^([-*+] \[[ xX]\]|#{1,6}|[-*+>]|\d+[.)])\s+`)

func descriptionPreview(description string) string {
	var text strings.Builder
	runes := 0
lines:
	for line := range strings.Lines(runePrefix(description, previewSource)) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") {
			continue
		}
		if marker := markdownMarker.FindStringIndex(line); marker != nil {
			line = line[marker[1]:]
		}
		for word := range strings.FieldsSeq(line) {
			if text.Len() != 0 {
				text.WriteByte(' ')
				runes++
			}
			text.WriteString(word)
			runes += utf8.RuneCountInString(word)
			if runes > PreviewRunes {
				break lines
			}
		}
	}
	preview := text.String()
	short := runePrefix(preview, PreviewRunes)
	if len(short) == len(preview) {
		return preview
	}
	if i := strings.LastIndexByte(short, ' '); i >= 0 && utf8.RuneCountInString(short[:i]) > PreviewRunes/2 {
		short = short[:i]
	}
	return strings.TrimRight(short, " .,;:") + "…"
}

func runePrefix(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}

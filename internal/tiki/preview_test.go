package tiki

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkDescriptionPreview(b *testing.B) {
	for _, size := range []int{140, MaxDescriptionBytes} {
		description := strings.Repeat("Description with words.\n", size/24+1)
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				descriptionPreview(description)
			}
		})
	}
}

func TestPreviewUsesSameSourceForDetailsAndLists(t *testing.T) {
	s, user := fixture(t)
	for _, description := range []string{
		strings.Repeat(" ", previewSource) + "Beyond the excerpt",
		strings.Repeat("# Heading\n", 1000),
		strings.Repeat("界", 60) + " " + strings.Repeat("界", 1000),
	} {
		item, err := s.Create(t.Context(), user.ID, CreateItem{Title: "Preview", Description: description})
		if err != nil {
			t.Fatal(err)
		}
		page, err := s.List(t.Context(), Filter{})
		if err != nil {
			t.Fatal(err)
		}
		listed := page.Items[len(page.Items)-1]
		if listed.Preview != item.Preview || item.Description != description {
			t.Fatalf("detail/list disagree: %q vs %q", item.Preview, listed.Preview)
		}
	}
	wide := strings.Repeat("界", 60) + " " + strings.Repeat("界", 100)
	if got := descriptionPreview(wide); got != string([]rune(wide)[:PreviewRunes])+"…" {
		t.Fatalf("word boundary counted bytes instead of runes: %q", got)
	}
}

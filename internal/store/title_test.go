package store

import (
	"strings"
	"testing"
)

func TestDeriveTitle(t *testing.T) {
	cases := []struct{ name, content, want string }{
		{"bold what-line", "**What**: Replaced X with Y\n**Why**: scale", "What: Replaced X with Y"},
		{"skips blanks and heading marks", "\n\n## Edge-deploy audit 2026-09-01\nbody", "Edge-deploy audit 2026-09-01"},
		{"bullet", "- first bullet here\n- second", "first bullet here"},
		{"whitespace only", "   \n\t", "Untitled"},
	}
	for _, c := range cases {
		if got := deriveTitle(c.content); got != c.want {
			t.Errorf("%s: deriveTitle(%q) = %q, want %q", c.name, c.content, got, c.want)
		}
	}
	got := deriveTitle(strings.Repeat("word ", 30))
	if r := []rune(got); len(r) > titleDeriveLimit+1 || !strings.HasSuffix(got, "…") {
		t.Errorf("long line not truncated at a word boundary: %q (%d runes)", got, len(r))
	}
}

func TestAddObservationDerivesTitleWhenBlank(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateSession("s1", "engram", "/tmp"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	want := "recognize-card edge fn: fixed two deno check errors."
	id, err := s.AddObservation(AddObservationParams{
		SessionID: "s1", Type: "bugfix", Title: "   ",
		Content: want + "\n\n1. TS2339 — the shim hard-coded two chained .eq()",
		Project: "engram", Scope: "project",
	})
	if err != nil {
		t.Fatalf("AddObservation: %v", err)
	}
	obs, err := s.GetObservation(id)
	if err != nil {
		t.Fatalf("GetObservation: %v", err)
	}
	if obs.Title != want {
		t.Fatalf("title = %q, want derived %q", obs.Title, want)
	}
	// An update that blanks the title re-derives instead of persisting "".
	blank := ""
	upd, err := s.UpdateObservation(id, UpdateObservationParams{Title: &blank})
	if err != nil {
		t.Fatalf("UpdateObservation: %v", err)
	}
	if upd.Title != want {
		t.Fatalf("update with blank title = %q, want re-derived %q", upd.Title, want)
	}
}

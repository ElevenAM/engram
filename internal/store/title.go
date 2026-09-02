package store

import "strings"

// titleDeriveLimit caps a derived title so it stays a one-line search hit.
const titleDeriveLimit = 80

// deriveTitle builds a searchable title from the first meaningful line of
// content. Title is the primary FTS field and the only line shown in search
// results, so an observation saved with a blank title is invisible to lexical
// search even though its content is fine — MCP clients satisfy the schema's
// "required" with "" (164 of 810 rows in one project by 2026-09-02).
func deriveTitle(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.ReplaceAll(line, "**", "")
		line = strings.TrimLeft(strings.TrimSpace(line), "#*->•")
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		r := []rune(line)
		if len(r) <= titleDeriveLimit {
			return line
		}
		cut := string(r[:titleDeriveLimit])
		if i := strings.LastIndex(cut, " "); i > titleDeriveLimit/2 {
			cut = cut[:i]
		}
		return strings.TrimRight(cut, " ,;:—-") + "…"
	}
	return "Untitled"
}

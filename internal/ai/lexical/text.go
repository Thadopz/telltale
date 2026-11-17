package lexical

import (
	"strings"
	"unicode"
)

// Terms emits Han bigrams and intact identifiers for SQLite's unicode61 tokenizer.
func Terms(text string) []string {
	var terms []string
	var word []rune
	var han []rune
	flush := func() {
		if len(word) > 0 {
			terms = append(terms, string(word))
			word = nil
		}
		if len(han) == 1 {
			terms = append(terms, string(han))
		}
		for i := 1; i < len(han); i++ {
			terms = append(terms, string(han[i-1:i+1]))
		}
		han = nil
	}
	for _, r := range strings.ToLower(text) {
		if unicode.Is(unicode.Han, r) {
			if len(word) > 0 {
				flush()
			}
			han = append(han, r)
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			if len(han) > 0 {
				flush()
			}
			word = append(word, r)
		} else {
			flush()
		}
	}
	flush()
	return terms
}

func Text(content string, metadata map[string]any) string {
	parts := []string{content}
	for _, key := range []string{"_context_prefix", "_source", "_section_path", "title", "heading2", "heading3"} {
		if value, ok := metadata[key].(string); ok {
			parts = append(parts, value)
		}
	}
	return strings.Join(Terms(strings.Join(parts, "\n")), " ")
}

// Match quotes terms so user punctuation and FTS operators never become query syntax.
func Match(query string) string {
	seen := map[string]bool{}
	var quoted []string
	for _, term := range Terms(query) {
		if !seen[term] {
			seen[term] = true
			quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
		}
		if len(quoted) >= 64 {
			break
		}
	}
	return strings.Join(quoted, " OR ")
}

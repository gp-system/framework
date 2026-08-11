package generator

import (
	"fmt"
	"go/token"
	"regexp"
	"strings"
	"unicode"
)

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// identRe matches a legal Go identifier body (letter then letters/digits),
// allowing the internal uppercase that camelCase / PascalCase names carry.
var identRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]*$`)

// snakeNameRe matches a normalized snake_case identifier (e.g. "add_blog"):
// lowercase words of letters/digits joined by single underscores. It rejects
// path separators, dots and anything else that could escape a directory.
var snakeNameRe = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// ValidateName enforces the naming rule for modules and projects: lowercase
// letters and digits, starting with a letter. This keeps the name usable
// verbatim as a Go package name, directory name and URL segment.
func ValidateName(kind, name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("%s name %q must match %s (lowercase, no separators; it becomes a Go package name)", kind, name, nameRe)
	}
	return nil
}

// ValidateSnakeName validates a normalized snake_case name (as produced by
// ToSnake) that becomes part of a generated filename. It rejects path
// separators, dots and other traversal vectors so a crafted name cannot
// escape the target directory.
func ValidateSnakeName(kind, name string) error {
	if !snakeNameRe.MatchString(name) {
		return fmt.Errorf("%s name %q must be lowercase words of letters/digits (no path separators, dots or leading digit)", kind, name)
	}
	return nil
}

// ValidateIdentifier checks that a normalized name (camelCase or PascalCase,
// as produced by ToCamel/ToPascal) is a legal Go identifier and not a Go
// keyword. It rejects path separators, dots and symbols so an operation,
// event, listener or job name cannot traverse into a crafted path or emit
// uncompilable code.
func ValidateIdentifier(kind, name string) error {
	if !identRe.MatchString(name) {
		return fmt.Errorf("%s name %q must be a letter followed by letters or digits (no separators, dots or symbols)", kind, name)
	}
	if token.IsKeyword(name) {
		return fmt.Errorf("%s name %q is a Go keyword", kind, name)
	}
	return nil
}

// splitCamel breaks a single word on camelCase/PascalCase boundaries, e.g.
// "getNews" -> ["get", "News"], "HTTPServer" -> ["HTTP", "Server"]. A
// letter-to-digit transition (e.g. "v2") is not a boundary. Runs of digits
// stay attached to the word they follow.
func splitCamel(w string) []string {
	runes := []rune(w)
	if len(runes) == 0 {
		return nil
	}
	var words []string
	start := 0
	for i := 1; i < len(runes); i++ {
		prev, cur := runes[i-1], runes[i]
		boundary := false
		switch {
		case unicode.IsUpper(cur) && (unicode.IsLower(prev) || unicode.IsDigit(prev)):
			// lower/digit -> upper, e.g. "get|News", "v2|Server"
			boundary = true
		case unicode.IsUpper(prev) && unicode.IsUpper(cur) && i+1 < len(runes) && unicode.IsLower(runes[i+1]):
			// acronym -> word, e.g. "HTTP|Server"
			boundary = true
		}
		if boundary {
			words = append(words, string(runes[start:i]))
			start = i
		}
	}
	words = append(words, string(runes[start:]))
	return words
}

// splitWords breaks kebab-case, snake_case, space-separated or
// camelCase/PascalCase input into its constituent words, e.g.
// "server-stats" -> ["server", "stats"] and "markAllRead" ->
// ["mark", "All", "Read"].
func splitWords(s string) []string {
	var words []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == '-' || r == '_' || r == ' '
	}) {
		words = append(words, splitCamel(part)...)
	}
	return words
}

// ToPascal converts kebab-case, snake_case, space-separated or camelCase
// input to PascalCase, e.g. for Go type and exported identifier names.
func ToPascal(s string) string {
	var b strings.Builder
	for _, w := range splitWords(s) {
		b.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}
	return b.String()
}

// ToCamel converts input to camelCase, e.g. for unexported identifier names.
func ToCamel(s string) string {
	p := ToPascal(s)
	if p == "" {
		return ""
	}
	return strings.ToLower(p[:1]) + p[1:]
}

// ToSnake converts input to snake_case, e.g. for file and path names. Each
// word is lowercased individually (after camelCase splitting), so
// "markAllRead" becomes "mark_all_read", not "markallread".
func ToSnake(s string) string {
	words := splitWords(s)
	lower := make([]string, len(words))
	for i, w := range words {
		lower[i] = strings.ToLower(w)
	}
	return strings.Join(lower, "_")
}

// ToKebab converts input to kebab-case, e.g. for CLI-facing names.
func ToKebab(s string) string {
	words := splitWords(s)
	lower := make([]string, len(words))
	for i, w := range words {
		lower[i] = strings.ToLower(w)
	}
	return strings.Join(lower, "-")
}

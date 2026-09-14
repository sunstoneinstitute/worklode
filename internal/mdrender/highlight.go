package mdrender

import (
	"regexp"
	"sort"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
)

// classPrefix namespaces every class Chroma emits. Chroma's own names are one
// to three letters ("k", "s", "nf"), and the cockpit's stylesheet already uses
// names that short for its own layout hooks (.t, .s, .tl), so unprefixed
// output would collide with them. The prefix also makes the sanitiser rule
// below something an author cannot spoof with an ordinary class name.
const classPrefix = "hl-"

// highlightExt colours fenced code blocks server-side, into classes rather
// than inline style attributes: the cockpit's CSP is style-src 'self' (see
// internal/api/web.go), so a style attribute would be dropped by the browser
// even if the sanitiser kept it. The colours live in app.tailwind.css.
//
// WithAllClasses gives every token type its own class. Without it Chroma emits
// only the classes the chosen style defines and collapses the rest to a
// parent, which would make the stylesheet's rules depend on which style was
// passed. Nothing here depends on the style otherwise — with classes on, it
// picks class names, not colours.
var highlightExt = highlighting.NewHighlighting(
	highlighting.WithFormatOptions(
		chromahtml.WithClasses(true),
		chromahtml.WithAllClasses(true),
		chromahtml.ClassPrefix(classPrefix),
	),
)

// highlightClass is the class values buildPolicy allows on a highlighted
// block's elements, built from Chroma's own table so it cannot drift from what
// the formatter emits. It is the same kind of scoped reopening of class as
// calloutClass: an exact set of known values, not a general styling hook.
//
// A body author who writes <span class="hl-k"> by hand gets keyword colour in
// prose. That is cosmetic — these classes carry no URL and reach no script —
// and it is the price of not hand-maintaining a list of 80 token names.
var highlightClass = buildHighlightClass()

func buildHighlightClass() *regexp.Regexp {
	seen := map[string]bool{}
	names := make([]string, 0, len(chroma.StandardTypes))
	for _, name := range chroma.StandardTypes {
		// The empty entry is the "no class" token; anything non-word would
		// need escaping and Chroma has never emitted one.
		if name == "" || seen[name] || !isClassWord(name) {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	// Longest first so the alternation cannot match a prefix of a longer
	// name and leave the rest to fail against \z.
	sort.Slice(names, func(i, j int) bool {
		if len(names[i]) != len(names[j]) {
			return len(names[i]) > len(names[j])
		}
		return names[i] < names[j]
	})
	return regexp.MustCompile(`\A` + classPrefix + `(?:` + strings.Join(names, "|") + `)\z`)
}

func isClassWord(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// hujsonLexer aliases HuJSON to Chroma's JavaScript lexer. HuJSON is JSON plus
// // and /* */ comments and trailing commas, all of which JavaScript already
// lexes and Chroma's JSON lexer does not — its JSON lexer takes a block
// comment apart into one error token per character. Registering the alias is
// Chroma's documented extension point; the wrapper delegates everything but
// the name.
type hujsonLexer struct {
	chroma.Lexer
	config *chroma.Config
}

func (l hujsonLexer) Config() *chroma.Config { return l.config }

func init() {
	js := lexers.Get("javascript")
	if js == nil {
		return
	}
	lexers.Register(hujsonLexer{
		Lexer:  js,
		config: &chroma.Config{Name: "HuJSON", Aliases: []string{"hujson"}, Filenames: []string{"*.hujson"}},
	})
}

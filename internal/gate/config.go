// Package gate is the design authority gate (11-design-authority-gate.md §3,
// §4; 12-spec-refactoring-design-tree.md S4, S29, S30): which changed paths
// ask for a Spec: trailer, and what a trailer may say. It is pure so the CLI
// (in CI, offline) and the server's reconciler parse the same thing.
package gate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// DefaultTrailer is the key a declaration line starts with when the [gate]
// table names none.
const DefaultTrailer = "Spec:"

// Config is the [gate] table of .worklode/config.toml (11 §3). Paths are
// globstar patterns, Regex is for what a glob cannot say, Trailer is the key
// the PR body or commit message must carry.
type Config struct {
	Paths   []string `toml:"paths"`
	Regex   []string `toml:"regex"`
	Trailer string   `toml:"trailer"`
}

// Load reads the [gate] table from repoRoot/.worklode/config.toml. ok is
// false when the file or the table is absent: the gate is off (11 §3,
// "Without a [gate] table nothing runs").
func Load(repoRoot string) (Config, bool, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, ".worklode", "config.toml"))
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, false, nil
	}
	if err != nil {
		return Config{}, false, err
	}
	return Parse(data)
}

// Parse decodes the [gate] table out of a whole config file. Other keys are
// ignored; internal/cli owns them.
func Parse(data []byte) (Config, bool, error) {
	var file struct {
		Gate *Config `toml:"gate"`
	}
	if err := toml.Unmarshal(data, &file); err != nil {
		return Config{}, false, fmt.Errorf("parse .worklode/config.toml: %w", err)
	}
	if file.Gate == nil {
		return Config{}, false, nil
	}
	cfg := *file.Gate
	if cfg.Trailer == "" {
		cfg.Trailer = DefaultTrailer
	}
	// The key is matched with its punctuation: a trailer written "Spec"
	// would match no line at all and the gate would refuse every guarded
	// change.
	if !strings.HasSuffix(cfg.Trailer, ":") {
		return Config{}, false, fmt.Errorf("[gate] trailer %q must end in a colon, like %q", cfg.Trailer, DefaultTrailer)
	}
	if len(cfg.Paths) == 0 && len(cfg.Regex) == 0 {
		return Config{}, false, errors.New("[gate] names no paths and no regex")
	}
	if _, err := cfg.Guards(); err != nil {
		return Config{}, false, err
	}
	return cfg, true, nil
}

// Guards is the compiled form of Config's paths and regex.
type Guards struct {
	patterns []*regexp.Regexp
}

// Guards compiles the config. A bad regex is reported with its source.
func (c Config) Guards() (Guards, error) {
	var g Guards
	for _, p := range c.Paths {
		g.patterns = append(g.patterns, globRegexp(p))
	}
	for _, r := range c.Regex {
		re, err := regexp.Compile(r)
		if err != nil {
			return Guards{}, fmt.Errorf("[gate] regex %q: %w", r, err)
		}
		g.patterns = append(g.patterns, re)
	}
	return g, nil
}

// Match reports whether a repo-relative path is guarded.
func (g Guards) Match(path string) bool {
	for _, re := range g.patterns {
		if re.MatchString(path) {
			return true
		}
	}
	return false
}

// globRegexp turns a globstar pattern into an anchored regexp: `**` crosses
// slashes (`**/` also matches nothing, so `**/x` matches `x`), `*` and `?`
// stay inside one path segment.
func globRegexp(glob string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch {
		case c == '*' && i+1 < len(glob) && glob[i+1] == '*':
			i++
			if i+1 < len(glob) && glob[i+1] == '/' {
				i++
				b.WriteString(`(?:.*/)?`)
			} else {
				b.WriteString(`.*`)
			}
		case c == '*':
			b.WriteString(`[^/]*`)
		case c == '?':
			b.WriteString(`[^/]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

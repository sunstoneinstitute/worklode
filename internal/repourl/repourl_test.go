package repourl_test

import (
	"errors"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/repourl"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"scp style", "git@github.com:sunstoneinstitute/worklode.git", "sunstoneinstitute/worklode"},
		{"scp style no suffix", "git@github.com:sunstoneinstitute/worklode", "sunstoneinstitute/worklode"},
		{"scp style no user", "github.com:sunstoneinstitute/worklode.git", "sunstoneinstitute/worklode"},
		{"https", "https://github.com/sunstoneinstitute/worklode", "sunstoneinstitute/worklode"},
		{"https with suffix", "https://github.com/sunstoneinstitute/worklode.git", "sunstoneinstitute/worklode"},
		{"git+ssh", "git+ssh://git@github.com/sunstoneinstitute/worklode.git", "sunstoneinstitute/worklode"},
		{"ssh with port", "ssh://git@github.com:22/sunstoneinstitute/worklode.git", "sunstoneinstitute/worklode"},
		{"git protocol", "git://github.com/sunstoneinstitute/worklode.git", "sunstoneinstitute/worklode"},
		{"bare owner/name", "sunstoneinstitute/worklode", "sunstoneinstitute/worklode"},
		{"trailing slash", "https://github.com/sunstoneinstitute/worklode/", "sunstoneinstitute/worklode"},
		{"surrounding space", "  git@github.com:sunstoneinstitute/worklode.git\n", "sunstoneinstitute/worklode"},
		{"other host", "git@git.example.com:sunstoneinstitute/worklode.git", "sunstoneinstitute/worklode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repourl.Normalize(tc.in)
			if err != nil {
				t.Fatalf("Normalize(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("Normalize(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeRejects(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"blank", "   "},
		{"one segment", "worklode"},
		{"three segments", "https://github.com/a/b/c"},
		{"empty owner", "https://github.com//worklode"},
		{"empty name", "https://github.com/sunstoneinstitute/"},
		{"scheme only", "https://github.com"},
		{"not a url", "this is not a url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repourl.Normalize(tc.in)
			if err == nil {
				t.Fatalf("Normalize(%q) = %q; want an error", tc.in, got)
			}
			if !errors.Is(err, repourl.ErrNotRepoURL) {
				t.Fatalf("Normalize(%q) error = %v; want ErrNotRepoURL", tc.in, err)
			}
		})
	}
}

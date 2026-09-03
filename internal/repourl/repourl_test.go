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

// TestHost covers WL-269: the host component `lode graph derive` puts in the repo
// instance IRI, extracted from the same remote forms Normalize accepts.
func TestHost(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"scp style", "git@github.com:sunstoneinstitute/worklode.git", "github.com"},
		{"scp style no user", "github.com:sunstoneinstitute/worklode.git", "github.com"},
		{"https", "https://github.com/sunstoneinstitute/worklode", "github.com"},
		{"ssh with port", "ssh://git@github.com:22/sunstoneinstitute/worklode.git", "github.com"},
		{"git+ssh", "git+ssh://git@github.com/sunstoneinstitute/worklode.git", "github.com"},
		{"other forge", "git@git.example.com:sunstoneinstitute/worklode.git", "git.example.com"},
		{"self-hosted gitlab", "https://gitlab.internal.example:8443/team/tool.git", "gitlab.internal.example"},
		{"bare owner/name has no host", "sunstoneinstitute/worklode", ""},
		{"surrounding space", "  git@github.com:sunstoneinstitute/worklode.git\n", "github.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repourl.Host(tc.in)
			if err != nil {
				t.Fatalf("Host(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("Host(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}

	if _, err := repourl.Host(""); !errors.Is(err, repourl.ErrNotRepoURL) {
		t.Fatalf("Host(\"\") err = %v; want ErrNotRepoURL", err)
	}
	if _, err := repourl.Host("https://github.com"); !errors.Is(err, repourl.ErrNotRepoURL) {
		t.Fatalf("Host with no path err = %v; want ErrNotRepoURL", err)
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

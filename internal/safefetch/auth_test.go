// Credential scoping (WithBearer). The property under test is not "the header
// is sent" but "the header is sent to exactly the hosts it was bound to" — a
// fetch reaches attacker-chosen URLs, and every other host in the allowlist,
// including a redirect target, is somewhere the credential must not go.

package safefetch_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/safefetch"
)

// authRecorder is an origin that remembers the Authorization header of every
// request it served. Mutex-guarded: the handler runs on the server's
// goroutine, the assertion on the test's.
type authRecorder struct {
	mu   sync.Mutex
	seen []string
	URL  string
}

// newAuthRecorder serves "ok" from a loopback origin, optionally redirecting
// to redirectTo first. URL is the "localhost" spelling of its address.
func newAuthRecorder(t *testing.T, redirectTo string) *authRecorder {
	t.Helper()
	rec := &authRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.seen = append(rec.seen, r.Header.Get("Authorization"))
		rec.mu.Unlock()
		if redirectTo != "" {
			http.Redirect(w, r, redirectTo, http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	rec.URL = loopbackURL(t, srv)
	return rec
}

// rawURL is the origin's address under its 127.0.0.1 spelling, which is a
// different host from "localhost" for scoping purposes even though it is the
// same server.
func (a *authRecorder) rawURL() string {
	return strings.Replace(a.URL, "localhost", "127.0.0.1", 1)
}

func (a *authRecorder) headers() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.seen...)
}

// anyHostFetcher reaches both the "localhost" and the "127.0.0.1" spelling of
// an httptest origin, so a test can move a fetch between two host strings the
// guard both permits.
func anyHostFetcher() *safefetch.Fetcher {
	return safefetch.NewForTest(nil, 1<<20, safefetch.TestEscapes{Loopback: true, AnyHost: true})
}

func TestBearerReachesNamedHost(t *testing.T) {
	origin := newAuthRecorder(t, "")
	f := loopbackFetcher(1<<20).WithBearer([]string{"localhost"}, "tok123")

	if _, _, err := f.Get(context.Background(), origin.URL); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := origin.headers(); len(got) != 1 || got[0] != "Bearer tok123" {
		t.Fatalf("Authorization headers = %q, want one \"Bearer tok123\"", got)
	}
}

// The credential is bound to a host, not to the Fetcher: a fetch the allowlist
// permits but the credential does not name goes out bare.
func TestBearerNotSentToUnnamedHost(t *testing.T) {
	origin := newAuthRecorder(t, "")
	f := anyHostFetcher().WithBearer([]string{"localhost"}, "tok123")

	if _, _, err := f.Get(context.Background(), origin.rawURL()); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := origin.headers(); len(got) != 1 || got[0] != "" {
		t.Fatalf("Authorization headers = %q, want one empty", got)
	}
}

// A redirect off the credential's host drops it, even though the guard still
// permits the target. net/http would drop it here too, so this pins the
// outcome rather than the mechanism; TestBearerReattachedOnRedirectBack is the
// one that fails if the header stops being decided per hop.
func TestBearerDroppedOnRedirectToOtherHost(t *testing.T) {
	target := newAuthRecorder(t, "")
	src := newAuthRecorder(t, target.rawURL())
	f := anyHostFetcher().WithBearer([]string{"localhost"}, "tok123")

	if _, _, err := f.Get(context.Background(), src.URL); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := src.headers(); len(got) != 1 || got[0] != "Bearer tok123" {
		t.Fatalf("source Authorization headers = %q, want one \"Bearer tok123\"", got)
	}
	if got := target.headers(); len(got) != 1 || got[0] != "" {
		t.Fatalf("redirect target saw Authorization %q, want one empty", got)
	}
}

// A redirect back onto the credential's host re-attaches it: the check is a
// property of the host being contacted, not a one-way latch. This is what an
// implementation that sets the header once on the outgoing request cannot do
// -- it starts off-scope, so it would send nothing at all.
func TestBearerReattachedOnRedirectBack(t *testing.T) {
	target := newAuthRecorder(t, "")
	src := newAuthRecorder(t, target.URL)
	f := anyHostFetcher().WithBearer([]string{"localhost"}, "tok123")

	if _, _, err := f.Get(context.Background(), src.rawURL()); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := src.headers(); len(got) != 1 || got[0] != "" {
		t.Fatalf("source Authorization headers = %q, want one empty", got)
	}
	if got := target.headers(); len(got) != 1 || got[0] != "Bearer tok123" {
		t.Fatalf("redirect target Authorization = %q, want one \"Bearer tok123\"", got)
	}
}

// The credential's host scope is label-aligned like the allowlist, so a suffix
// that is not a whole label ("ocalhost" of "localhost") matches nothing.
func TestBearerHostScopeIsLabelAligned(t *testing.T) {
	origin := newAuthRecorder(t, "")
	f := anyHostFetcher().WithBearer([]string{"ocalhost"}, "tok123")

	if _, _, err := f.Get(context.Background(), origin.URL); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := origin.headers(); len(got) != 1 || got[0] != "" {
		t.Fatalf("Authorization headers = %q, want one empty", got)
	}
}

// A caller whose credential lookup came back empty needs no branch of its own:
// WithBearer("") is an unauthenticated Fetcher, not a "Bearer " header.
func TestBearerWithNothingToSendIsUnauthenticated(t *testing.T) {
	for name, apply := range map[string]func(*safefetch.Fetcher) *safefetch.Fetcher{
		"empty token": func(f *safefetch.Fetcher) *safefetch.Fetcher {
			return f.WithBearer([]string{"localhost"}, "")
		},
		"no hosts": func(f *safefetch.Fetcher) *safefetch.Fetcher {
			return f.WithBearer(nil, "tok123")
		},
		"credential cleared": func(f *safefetch.Fetcher) *safefetch.Fetcher {
			return f.WithBearer([]string{"localhost"}, "tok123").WithBearer(nil, "")
		},
	} {
		t.Run(name, func(t *testing.T) {
			origin := newAuthRecorder(t, "")
			if _, _, err := apply(loopbackFetcher(1<<20)).Get(context.Background(), origin.URL); err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got := origin.headers(); len(got) != 1 || got[0] != "" {
				t.Fatalf("Authorization headers = %q, want one empty", got)
			}
		})
	}
}

// Userinfo in the URL is an Authorization header nobody in this package asked
// for: http.Client turns it into a Basic header and then applies its own
// forwarding rule to it. An unauthenticated Fetcher sends no Authorization at
// all, whatever the URL says.
func TestUserinfoDoesNotBecomeAnAuthorizationHeader(t *testing.T) {
	origin := newAuthRecorder(t, "")
	withUser := strings.Replace(origin.URL, "http://", "http://user:pass@", 1)

	if _, _, err := loopbackFetcher(1<<20).Get(context.Background(), withUser); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := origin.headers(); len(got) != 1 || got[0] != "" {
		t.Fatalf("Authorization headers = %q, want one empty", got)
	}
}

// WithBearer returns a copy: the Fetcher it was called on stays
// unauthenticated, so a shared base fetcher cannot pick up one caller's token.
func TestWithBearerDoesNotMutateReceiver(t *testing.T) {
	origin := newAuthRecorder(t, "")
	base := loopbackFetcher(1 << 20)
	_ = base.WithBearer([]string{"localhost"}, "tok123")

	if _, _, err := base.Get(context.Background(), origin.URL); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := origin.headers(); len(got) != 1 || got[0] != "" {
		t.Fatalf("Authorization headers = %q, want one empty", got)
	}
}

package safefetch_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/safefetch"
)

func TestRejectsBadTargets(t *testing.T) {
	f := safefetch.New([]string{"githubusercontent.com"}, 1<<20)
	for _, url := range []string{
		"http://user-images.githubusercontent.com/x.png", // not https
		"https://evil.example/x.png",                     // host not allowed
		"https://169.254.169.254/latest/meta-data",       // metadata, and host not allowed
		"file:///etc/passwd",
		"https://localhost/x.png",
	} {
		if _, _, err := f.Get(context.Background(), url); err == nil {
			t.Fatalf("%s: expected rejection", url)
		}
	}
}

func TestAllowsSuffixMatchOnly(t *testing.T) {
	f := safefetch.New([]string{"githubusercontent.com"}, 1<<20)
	// A lookalike host must not pass: the suffix check has to be
	// label-aligned, not a substring test.
	if _, _, err := f.Get(context.Background(), "https://evilgithubusercontent.com/x.png"); err == nil {
		t.Fatal("lookalike host accepted")
	}
}

func TestFetchesAndCaps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", 100)))
	}))
	defer srv.Close()

	// AllowLoopbackForTest lets the guard reach httptest; production never
	// sets it.
	f := safefetch.New(nil, 10)
	f.AllowLoopbackForTest = true
	f.AllowAnyHostForTest = true
	if _, _, err := f.Get(context.Background(), srv.URL); err == nil {
		t.Fatal("expected size cap to reject a 100-byte body with a 10-byte limit")
	}
}

// --- adversarial cases beyond the plan ---

// loopbackURL rewrites an httptest URL to use the "localhost" name so the host
// allowlist (which never matches a bare IP literal) can be exercised.
func loopbackURL(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	u := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	if u == srv.URL {
		t.Fatalf("unexpected httptest URL %q", srv.URL)
	}
	return u
}

func loopbackFetcher(maxBytes int64) *safefetch.Fetcher {
	f := safefetch.New([]string{"localhost"}, maxBytes)
	f.AllowLoopbackForTest = true
	return f
}

func TestFetchesBodyAndContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("\x89PNG\r\n\x1a\n payload"))
	}))
	defer srv.Close()

	data, ct, err := loopbackFetcher(1<<20).Get(context.Background(), loopbackURL(t, srv))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.HasPrefix(string(data), "\x89PNG") {
		t.Fatalf("body = %q", data)
	}
	if ct != "image/png" {
		t.Fatalf("content type = %q", ct)
	}
}

func TestRejectsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	if _, _, err := loopbackFetcher(1<<20).Get(context.Background(), loopbackURL(t, srv)); err == nil {
		t.Fatal("expected 404 to be an error")
	}
}

// A body with no Content-Length (chunked) must still be capped: the limit
// comes from the reader, never from a header the origin controls.
func TestCapsChunkedBodyWithoutContentLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", 50)))
		w.(http.Flusher).Flush()
		w.Write([]byte(strings.Repeat("x", 50)))
	}))
	defer srv.Close()

	if _, _, err := loopbackFetcher(10).Get(context.Background(), loopbackURL(t, srv)); err == nil {
		t.Fatal("expected chunked over-limit body to be rejected")
	}
}

// A lying (too small) Content-Length must not let extra bytes through.
func TestIgnoresBytesBeyondContentLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprintf(buf, "HTTP/1.1 200 OK\r\nContent-Type: image/png\r\nContent-Length: 5\r\nConnection: close\r\n\r\n%s",
			strings.Repeat("x", 100))
		buf.Flush()
	}))
	defer srv.Close()

	data, _, err := loopbackFetcher(10).Get(context.Background(), loopbackURL(t, srv))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(data) != 5 {
		t.Fatalf("read %d bytes, want 5", len(data))
	}
}

// redirectChain serves /0../hops, each hop redirecting to the next; the last
// returns a body. It returns the URL of hop 0 under the "localhost" name.
func redirectChain(t *testing.T, hops int) string {
	t.Helper()
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		if n < hops {
			http.Redirect(w, r, fmt.Sprintf("%s/%d", base, n+1), http.StatusFound)
			return
		}
		w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	base = loopbackURL(t, srv)
	return base + "/0"
}

// The spec caps redirects at 3, so a 3-hop chain resolves and a 4-hop one does
// not.
func TestRedirectCap(t *testing.T) {
	if _, _, err := loopbackFetcher(1<<20).Get(context.Background(), redirectChain(t, 3)); err != nil {
		t.Fatalf("3 redirects should be allowed: %v", err)
	}
	if _, _, err := loopbackFetcher(1<<20).Get(context.Background(), redirectChain(t, 4)); err == nil {
		t.Fatal("4 redirects should be rejected")
	}
}

// A cross-origin redirect is re-checked, and passes when the target is still
// allowed.
func TestRedirectToOtherAllowedOriginPasses(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer target.Close()
	targetURL := loopbackURL(t, target)

	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetURL, http.StatusFound)
	}))
	defer src.Close()

	data, _, err := loopbackFetcher(1<<20).Get(context.Background(), loopbackURL(t, src))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(data) != "ok" {
		t.Fatalf("body = %q", data)
	}
}

func TestRedirectToDisallowedTargetIsRejected(t *testing.T) {
	for name, target := range map[string]string{
		"ip literal bypasses no allowlist": "http://127.0.0.1:1/x.png",
		"metadata service":                 "https://169.254.169.254/latest/meta-data",
		"other host":                       "https://evil.example/x.png",
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target, http.StatusFound)
			}))
			defer srv.Close()

			if _, _, err := loopbackFetcher(1<<20).Get(context.Background(), loopbackURL(t, srv)); err == nil {
				t.Fatalf("redirect to %s accepted", target)
			}
		})
	}
}

func TestRejectsNonDefaultPort(t *testing.T) {
	f := safefetch.New([]string{"githubusercontent.com"}, 1<<20)
	_, _, err := f.Get(context.Background(), "https://user-images.githubusercontent.com:22/x.png")
	if err == nil {
		t.Fatal("non-default port accepted")
	}
	if !strings.Contains(err.Error(), "port") {
		t.Fatalf("want a port error, got %v", err)
	}
	// 443 is the default and stays allowed; it gets as far as resolution.
	_, _, err = f.Get(context.Background(), "https://nope.githubusercontent.com:443/x.png")
	if err != nil && strings.Contains(err.Error(), "port") {
		t.Fatalf("explicit :443 rejected: %v", err)
	}
}

func TestRejectsMalformedAndConfusingURLs(t *testing.T) {
	f := safefetch.New([]string{"githubusercontent.com"}, 1<<20)
	for name, raw := range map[string]string{
		// u.Hostname() is evil.example here; the allowlist must see that,
		// not the userinfo that looks like an allowed host.
		"userinfo confusion":   "https://user-images.githubusercontent.com@evil.example/x.png",
		"empty host":           "https:///x.png",
		"newline in url":       "https://user-images.githubusercontent.com/x.png\n",
		"null byte in url":     "https://user-images.githubusercontent.com/x\x00.png",
		"null escape in host":  "https://evil.example%00.githubusercontent.com/x.png",
		"non-ascii host":       "https://exämple.githubusercontent.com/x.png",
		"homograph host":       "https://githubusercontent.cоm/x.png",
		"trailing dot on evil": "https://evil.example./x.png",
		"bare ip literal":      "https://93.184.216.34/x.png",
		"ipv6 literal":         "https://[2606:4700:4700::1111]/x.png",
		"double slash host":    "https://evil.example//user-images.githubusercontent.com/x.png",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := f.Get(context.Background(), raw); err == nil {
				t.Fatalf("%q accepted", raw)
			}
		})
	}
}

// A trailing root dot on an allowed host is normalised away rather than
// treated as a different host.
func TestTrailingDotOnAllowedHostNormalises(t *testing.T) {
	f := safefetch.New([]string{"example.invalid"}, 1<<20)
	for _, raw := range []string{
		"https://img.example.invalid./x.png",
		"https://IMG.Example.INVALID./x.png",
	} {
		_, _, err := f.Get(context.Background(), raw)
		if err == nil {
			t.Fatalf("%s: .invalid should not resolve", raw)
		}
		if strings.Contains(err.Error(), "not allowed") {
			t.Fatalf("%s: rejected by the allowlist, want a resolve failure: %v", raw, err)
		}
	}
}

// An IP literal can never satisfy a domain allowlist, and when the allowlist is
// bypassed for tests the blocked-range table still has to reject it.
func TestIPLiteralNeverSatisfiesAllowlist(t *testing.T) {
	f := safefetch.New([]string{"githubusercontent.com"}, 1<<20)
	if _, _, err := f.Get(context.Background(), "https://169.254.169.254/x"); err == nil {
		t.Fatal("metadata IP accepted")
	}

	any := safefetch.New(nil, 1<<20)
	any.AllowAnyHostForTest = true
	_, _, err := any.Get(context.Background(), "https://169.254.169.254/x")
	if err == nil || !strings.Contains(err.Error(), "blocked address range") {
		t.Fatalf("want a blocked-range error, got %v", err)
	}
}

// localhost must fail on the address check too, not only the allowlist.
func TestLoopbackNameFailsAddressCheck(t *testing.T) {
	f := safefetch.New(nil, 1<<20)
	f.AllowAnyHostForTest = true
	_, _, err := f.Get(context.Background(), "https://localhost/x.png")
	if err == nil || !strings.Contains(err.Error(), "blocked address range") {
		t.Fatalf("want a blocked-range error for localhost, got %v", err)
	}
}

// GCP's metadata name resolves to 169.254.169.254 where it resolves at all;
// either way it must never be fetched.
func TestCloudMetadataNameRejected(t *testing.T) {
	f := safefetch.New(nil, 1<<20)
	f.AllowAnyHostForTest = true
	if _, _, err := f.Get(context.Background(), "https://metadata.google.internal/computeMetadata/v1/"); err == nil {
		t.Fatal("metadata.google.internal accepted")
	}
}

// One representative address per blocked range. IPv4-mapped IPv6 forms are
// included because Go's net.IP predicates do not all normalise them.
func TestBlockedRanges(t *testing.T) {
	f := safefetch.New(nil, 1<<20)
	f.AllowAnyHostForTest = true

	for _, ip := range []string{
		// IPv4
		"0.0.0.0", "0.1.2.3",
		"10.0.0.1",
		"100.64.0.1",
		"127.0.0.1", "127.1.2.3",
		"169.254.169.254",
		"172.16.0.1",
		"192.0.0.1",
		"192.0.2.1",
		"192.88.99.1",
		"192.168.1.1",
		"198.18.0.1",
		"198.51.100.1",
		"203.0.113.1",
		"224.0.0.1",
		"240.0.0.1",
		"255.255.255.255",
		// IPv6
		"[::]",
		"[::1]",
		"[::7f00:1]",
		"[::ffff:127.0.0.1]",
		"[::ffff:169.254.169.254]",
		"[::ffff:10.0.0.1]",
		"[64:ff9b::7f00:1]",
		"[64:ff9b:1::7f00:1]",
		"[100::1]",
		"[2001::1]",
		"[2001:2::1]",
		"[2001:20::1]",
		"[2001:db8::1]",
		"[2002:7f00:1::1]",
		"[3fff::1]",
		"[5f00::1]",
		"[fc00::1]",
		"[fd00:ec2::254]",
		"[fe80::1]",
		"[ff02::1]",
	} {
		t.Run(ip, func(t *testing.T) {
			_, _, err := f.Get(context.Background(), "https://"+ip+"/x.png")
			if err == nil || !strings.Contains(err.Error(), "blocked address range") {
				t.Fatalf("want a blocked-range error, got %v", err)
			}
		})
	}
}

// The complement: ordinary public addresses are not in the table. A cancelled
// context stops the request at the dial, so the assertion needs no network.
func TestPublicAddressesNotBlocked(t *testing.T) {
	f := safefetch.New(nil, 1<<20)
	f.AllowAnyHostForTest = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for _, ip := range []string{"8.8.8.8", "93.184.216.34", "1.1.1.1", "[2606:4700:4700::1111]", "[2a00:1450:4001::200e]"} {
		t.Run(ip, func(t *testing.T) {
			_, _, err := f.Get(ctx, "https://"+ip+"/x.png")
			if err != nil && strings.Contains(err.Error(), "blocked address range") {
				t.Fatalf("%s wrongly blocked: %v", ip, err)
			}
		})
	}
}

// Get wraps ctx with the whole-fetch timeout before calling checkURL, not
// after, so an already-expired context is honoured by the pre-flight resolve
// too, not only by the later HTTP round trip. Before the fix, checkURL ran on
// the caller's raw context and a stalling nameserver could run past the
// documented 30-second budget.
func TestPreflightHonoursExpiredContext(t *testing.T) {
	f := safefetch.New([]string{"githubusercontent.com"}, 1<<20)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Minute))
	defer cancel()

	start := time.Now()
	_, _, err := f.Get(ctx, "https://user-images.githubusercontent.com/x.png")
	if err == nil {
		t.Fatal("expected an error for an already-expired context")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("preflight did not fail fast on an expired context: %v", elapsed)
	}
}

// A host with an empty label — a leading dot, or ".." anywhere — must be
// rejected by the host check itself: strings.HasSuffix(host, "."+a) would
// otherwise be true for a host that is nothing but a blank label glued onto
// an allowed suffix.
func TestRejectsEmptyLabelHosts(t *testing.T) {
	f := safefetch.New([]string{"githubusercontent.com"}, 1<<20)
	for name, raw := range map[string]string{
		"leading dot": "https://.githubusercontent.com/x",
		"double dot":  "https://evil.example..githubusercontent.com/x",
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := f.Get(context.Background(), raw)
			if err == nil {
				t.Fatalf("%q accepted", raw)
			}
			if !strings.Contains(err.Error(), "empty label") {
				t.Fatalf("%q: want rejection by the host check, got %v", raw, err)
			}
		})
	}
}

// The two natural spellings of "and its subdomains" — a leading dot and a
// leading "*." — must allow the same hosts as the bare suffix, not silently
// allow nothing. A trailing dot on the allowlist entry must keep working too.
func TestHostAllowedEntrySpellings(t *testing.T) {
	for _, entry := range []string{
		"githubusercontent.com",
		".githubusercontent.com",
		"*.githubusercontent.com",
		"githubusercontent.com.",
	} {
		t.Run(entry, func(t *testing.T) {
			f := safefetch.New([]string{entry}, 1<<20)
			if _, _, err := f.Get(context.Background(), "https://a.githubusercontent.com/x.png"); err != nil && strings.Contains(err.Error(), "not allowed") {
				t.Fatalf("entry %q: allowed host rejected: %v", entry, err)
			}
			if _, _, err := f.Get(context.Background(), "https://evilgithubusercontent.com/x.png"); err == nil {
				t.Fatalf("entry %q: lookalike host accepted", entry)
			}
		})
	}
}

// A second trailing dot must not be silently absorbed: the dialer uses
// u.Hostname() verbatim, so normalising away more than the one legal root dot
// would validate a name other than the one actually dialled.
func TestDoubleTrailingDotRejected(t *testing.T) {
	f := safefetch.New([]string{"githubusercontent.com"}, 1<<20)
	if _, _, err := f.Get(context.Background(), "https://githubusercontent.com../x"); err == nil {
		t.Fatal("double trailing dot accepted")
	}
}

// An allowlist with no usable entries must still fail closed: it allows
// nothing, not everything.
func TestEmptyAllowlistEntriesAllowNothing(t *testing.T) {
	for _, hosts := range [][]string{nil, {}, {""}, {"."}} {
		t.Run(fmt.Sprintf("%q", hosts), func(t *testing.T) {
			f := safefetch.New(hosts, 1<<20)
			_, _, err := f.Get(context.Background(), "https://a.githubusercontent.com/x.png")
			if err == nil || !strings.Contains(err.Error(), "not allowed") {
				t.Fatalf("want a not-allowed rejection, got %v", err)
			}
		})
	}
}

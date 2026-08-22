// Package safefetch performs outbound HTTP GETs on attacker-influenced URLs.
//
// Threat model: importing a GitHub issue body mirrors the images it references
// (spec 021 §12), so the URL is chosen by whoever filed the issue. An
// unguarded fetch is an SSRF primitive against everything the server can reach
// — cloud metadata, cluster-internal services, Postgres. The guard is
// https-only, a label-aligned host allowlist, the default port, a table of
// blocked address ranges, a 3-hop redirect cap with the full check reapplied
// per hop, a byte cap, and a 30-second whole-fetch timeout. Every failure is
// an error the caller is expected to treat as non-fatal.
//
// A fetch may carry a credential (WithBearer) — private-repo images need the
// installation's GitHub App token — but the credential is bound to the hosts
// it belongs to, not to the fetch. The allowlist is deliberately wider than
// any one credential's scope, and a redirect can move a fetch across it, so
// the header is decided per hop against the host actually being contacted.
//
// The address check runs twice on purpose. The pre-flight resolve gives a fast,
// legible error, but it is not a boundary: http.Transport resolves the host
// again at dial time, so a name that answers with a public address for the
// pre-flight lookup and 127.0.0.1 for the dial would walk straight through
// (DNS rebinding). The boundary is the dialer's Control hook, which sees the
// literal address the kernel is about to connect to.
package safefetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

const (
	// maxRedirects is the number of redirect hops permitted; the 4th is
	// refused.
	maxRedirects = 3
	fetchTimeout = 30 * time.Second
)

// blockedPrefixes are the ranges no fetch may reach. Expressed as a table
// rather than a pile of net.IP predicates because the predicates miss ranges
// that matter here: NAT64 (64:ff9b::/96) and 6to4 (2002::/16) embed arbitrary
// IPv4 addresses, including loopback and RFC 1918, and no predicate catches
// them. Addresses are unmapped before the lookup, so ::ffff:127.0.0.1 is
// tested as 127.0.0.1.
var blockedPrefixes = []netip.Prefix{
	// IPv4
	netip.MustParsePrefix("0.0.0.0/8"),       // this network; only 0.0.0.0 itself is IsUnspecified
	netip.MustParsePrefix("10.0.0.0/8"),      // private
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT
	netip.MustParsePrefix("127.0.0.0/8"),     // loopback
	netip.MustParsePrefix("169.254.0.0/16"),  // link-local, incl. 169.254.169.254 metadata
	netip.MustParsePrefix("172.16.0.0/12"),   // private
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("192.88.99.0/24"),  // 6to4 relay anycast
	netip.MustParsePrefix("192.168.0.0/16"),  // private
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("224.0.0.0/4"),     // multicast
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, incl. 255.255.255.255

	// IPv6
	netip.MustParsePrefix("::/96"),          // unspecified, ::1, IPv4-compatible
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64: wraps any IPv4, e.g. 64:ff9b::7f00:1 is 127.0.0.1
	netip.MustParsePrefix("64:ff9b:1::/48"), // local-use NAT64
	netip.MustParsePrefix("100::/64"),       // discard-only
	netip.MustParsePrefix("2001::/32"),      // Teredo
	netip.MustParsePrefix("2001:2::/48"),    // benchmarking
	netip.MustParsePrefix("2001:20::/28"),   // ORCHIDv2
	netip.MustParsePrefix("2001:db8::/32"),  // documentation
	netip.MustParsePrefix("2002::/16"),      // 6to4: wraps any IPv4
	netip.MustParsePrefix("3fff::/20"),      // documentation
	netip.MustParsePrefix("5f00::/16"),      // segment routing SIDs
	netip.MustParsePrefix("fc00::/7"),       // unique local, incl. fd00:ec2::254 metadata
	netip.MustParsePrefix("fe80::/10"),      // link-local
	netip.MustParsePrefix("ff00::/8"),       // multicast
}

var errBlockedIP = errors.New("blocked address range")

// Fetcher fetches remote content under the guard. The zero value is not
// usable; construct one with New.
//
// Every field is unexported and set once at construction: a Fetcher handed to
// production code cannot have its guard relaxed by assignment. The two
// relaxations tests need are reachable only through NewForTest, which refuses
// to run outside a test binary.
type Fetcher struct {
	allowedHosts []string
	maxBytes     int64

	// authHosts and authValue are the credential and the hosts it may be sent
	// to. Empty authValue means every request goes out unauthenticated, which
	// is what New builds; see WithBearer.
	authHosts []string
	authValue string

	// allowLoopback additionally permits http and a non-default port, because
	// httptest serves plain http on a random port. allowAnyHost drops the host
	// allowlist but keeps every address check.
	allowLoopback bool
	allowAnyHost  bool
}

// New returns a Fetcher allowing the given host suffixes, capped at maxBytes.
func New(allowedHosts []string, maxBytes int64) *Fetcher {
	return &Fetcher{allowedHosts: allowedHosts, maxBytes: maxBytes}
}

// WithBearer returns a copy of f that sends "Authorization: Bearer <token>" on
// requests to authHosts — matched by the same label-aligned suffix rule as the
// host allowlist — and on no other request. An empty token or an empty
// authHosts returns an unauthenticated copy, so a caller whose credential
// lookup failed needs no branch of its own.
//
// The scoping is per host, not per fetch, because the allowlist is wider than
// the set of hosts a credential belongs to: mirroring fetches from github.com
// and githubusercontent.com, and the GitHub App token belongs to neither in
// full (021 §12). A blanket "attach to every fetch" option would hand the
// token to whichever host the attacker-chosen URL named.
func (f *Fetcher) WithBearer(authHosts []string, token string) *Fetcher {
	c := *f
	c.authHosts, c.authValue = nil, ""
	if token != "" && len(authHosts) > 0 {
		c.authHosts, c.authValue = authHosts, "Bearer "+token
	}
	return &c
}

// Get fetches url, returning its bytes and the Content-Type the origin
// claimed. The header is unverified — callers that care must sniff the bytes.
func (f *Fetcher) Get(ctx context.Context, rawURL string) ([]byte, string, error) {
	// The timeout has to cover the pre-flight resolve in checkURL too, or a
	// stalling nameserver alone can hold the call open past the documented
	// budget.
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	if err := f.checkURL(ctx, rawURL); err != nil {
		return nil, "", err
	}

	transport := f.transport()
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: &authTransport{base: transport, hosts: f.authHosts, value: f.authValue},
		Timeout:   fetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// via holds the requests already sent, so its length is the
			// number of hops taken so far.
			if len(via) > maxRedirects {
				return fmt.Errorf("more than %d redirects", maxRedirects)
			}
			// Re-check every hop: a permitted host can redirect anywhere.
			return f.checkURL(req.Context(), req.URL.String())
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, "", fmt.Errorf("fetch %s: %s", rawURL, resp.Status)
	}
	// Read one byte past the cap so an over-limit body is detected rather
	// than silently truncated. Content-Length is never trusted for this.
	data, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > f.maxBytes {
		return nil, "", fmt.Errorf("fetch %s: body exceeds %d bytes", rawURL, f.maxBytes)
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// authTransport is the only thing that may put an Authorization header on a
// request this package sends: it attaches the credential when the host being
// contacted is one the credential names, and strips the header from every
// other request. It is installed unconditionally, so the invariant holds on an
// unauthenticated Fetcher too -- a URL carrying userinfo would otherwise have
// http.Client synthesise a Basic header out of an attacker's own credential
// and carry it across hops on net/http's terms rather than this package's.
//
// The decision belongs in a RoundTripper rather than on the outgoing request
// because a fetch does not stay on one host. The initial URL is chosen by
// whoever filed the issue, and can name any host the allowlist permits, which
// is wider than any credential's scope; each redirect hop can move it again,
// on or off scope. A RoundTripper sees every hop as its own request, so the
// question asked is always "is *this* host one the credential names" rather
// than "was the first one". net/http has a rule of its own here -- it drops
// Authorization on a redirect to anything but the original host or a
// subdomain of it -- but that rule is about a header it was handed, not about
// which hosts a credential belongs to, and it never re-attaches.
type authTransport struct {
	base  http.RoundTripper
	hosts []string
	value string
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// A RoundTripper must not modify the request it is handed.
	r := req.Clone(req.Context())
	r.Header.Del("Authorization")
	if t.value != "" && hostMatches(normalizeHost(r.URL.Hostname()), t.hosts) {
		r.Header.Set("Authorization", t.value)
	}
	return t.base.RoundTrip(r)
}

// transport dials through a Control hook that inspects the address the kernel
// is about to connect to. This is the check that survives DNS rebinding; the
// pre-flight resolve in checkURL is only there for a better error. Keep-alives
// are off so no response can arrive over a connection that predates the
// current check.
func (f *Fetcher) transport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: -1,
		Control: func(network, address string, _ syscall.RawConn) error {
			switch network {
			case "tcp4", "tcp6":
			default:
				return fmt.Errorf("network %q not allowed", network)
			}
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("parse dial address %q: %w", address, err)
			}
			if err := f.checkPort(int(ap.Port())); err != nil {
				return err
			}
			return f.checkAddr(ap.Addr())
		},
	}
	return &http.Transport{
		DialContext: dialer.DialContext,
		// No proxy: an http_proxy in the environment would send the request
		// to the proxy's address, defeating the dial-time check.
		Proxy:                 nil,
		DisableKeepAlives:     true,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		// Default is 10 MiB; a hostile origin sending oversized headers is a
		// cheap allocation amplifier. Also what makes HTTP/2's
		// MaxHeaderListSize non-zero, since it derives from this field.
		MaxResponseHeaderBytes: 64 << 10,
	}
}

// checkURL applies every non-socket rule, then pre-resolves the host so a
// blocked target fails before a connection is attempted.
func (f *Fetcher) checkURL(ctx context.Context, rawURL string) error {
	// url.Parse rejects raw control characters, so \n and \x00 in the URL
	// never reach the rules below.
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "https" && !(f.allowLoopback && u.Scheme == "http") {
		return fmt.Errorf("scheme %q not allowed", u.Scheme)
	}
	// Hostname() strips any userinfo, so
	// https://githubusercontent.com@evil.example/ is checked as evil.example.
	host := normalizeHost(u.Hostname())
	if host == "" {
		return errors.New("url has no host")
	}
	// An empty label (a leading dot, or ".." anywhere) makes host == "."+a
	// true for a host that is nothing but the allowed suffix with a blank
	// label glued on front, e.g. ".githubusercontent.com". Reject it here
	// rather than relying on the resolver to fail.
	if strings.HasPrefix(host, ".") || strings.Contains(host, "..") {
		return fmt.Errorf("host %q has an empty label", host)
	}
	// Non-ASCII hosts are refused rather than IDNA-normalised: nothing this
	// fetches is an IDN, and refusing removes homograph lookalikes of an
	// allowed host from the comparison entirely.
	if !isASCII(host) {
		return fmt.Errorf("host %q is not ascii", host)
	}
	if port := u.Port(); port != "" {
		n, err := net.LookupPort("tcp", port)
		if err != nil {
			return fmt.Errorf("port %q not allowed", port)
		}
		if err := f.checkPort(n); err != nil {
			return err
		}
	}
	// An IP literal can never satisfy a domain allowlist.
	if addr, err := netip.ParseAddr(host); err == nil {
		if !f.allowAnyHost {
			return fmt.Errorf("host %q not allowed", host)
		}
		return f.checkAddr(addr)
	}
	if !f.allowAnyHost && !f.hostAllowed(host) {
		return fmt.Errorf("host %q not allowed", host)
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("resolve %s: no addresses", host)
	}
	// Fail on any blocked answer, not just the one that would be dialled.
	for _, addr := range addrs {
		if err := f.checkAddr(addr); err != nil {
			return err
		}
	}
	return nil
}

func (f *Fetcher) checkPort(port int) error {
	if port == 443 || f.allowLoopback {
		return nil
	}
	return fmt.Errorf("port %d not allowed", port)
}

// checkAddr rejects an address that falls in any blocked range.
func (f *Fetcher) checkAddr(addr netip.Addr) error {
	addr = addr.Unmap().WithZone("")
	if !addr.IsValid() {
		return fmt.Errorf("invalid address: %w", errBlockedIP)
	}
	if f.allowLoopback && addr.IsLoopback() {
		return nil
	}
	for _, p := range blockedPrefixes {
		if p.Contains(addr) {
			return fmt.Errorf("%s is in %s: %w", addr, p, errBlockedIP)
		}
	}
	return nil
}

// hostAllowed reports whether host satisfies the fetch allowlist.
func (f *Fetcher) hostAllowed(host string) bool {
	return hostMatches(host, f.allowedHosts)
}

// hostMatches matches host against a list of suffixes on label boundaries:
// "githubusercontent.com" permits user-images.githubusercontent.com and
// rejects evilgithubusercontent.com. Both the fetch allowlist and the
// credential's host scope use it, so a credential can never be attached to a
// lookalike host the allowlist would itself reject.
func hostMatches(host string, suffixes []string) bool {
	if host == "" {
		return false
	}
	for _, a := range suffixes {
		a = normalizeHost(a)
		// "*.example.com" and ".example.com" are the two natural spellings
		// of "and its subdomains", but neither equals "example.com" as a
		// string, so left unstripped they match nothing and fail closed
		// silently. Reduce both to the bare suffix the checks below already
		// match subdomains of.
		a = strings.TrimPrefix(a, "*.")
		a = strings.TrimPrefix(a, ".")
		if a == "" {
			continue
		}
		if host == a || strings.HasSuffix(host, "."+a) {
			return true
		}
	}
	return false
}

// normalizeHost lowercases and drops a single trailing root dot, so
// "GithubUserContent.com." and "githubusercontent.com" compare equal and
// "evil.example." cannot slip past the allowlist as a distinct string.
// TrimSuffix rather than TrimRight: a single trailing dot is the only legal
// form (the DNS root), so a second one must survive normalisation and fail
// the comparison rather than being silently absorbed — u.Hostname() is
// dialled verbatim, so validating a name other than the one dialled would
// reopen the gap this check exists to close.
func normalizeHost(host string) string {
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

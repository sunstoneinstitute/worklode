package api

// mcpClient is a static public OAuth client of the MCP server
// (WL-REQ-1840). There is no dynamic client registration.
type mcpClient struct {
	// redirects maps an exact redirect URI to the destination text the
	// consent page shows.
	redirects map[string]string
}

var mcpClients = map[string]mcpClient{
	"worklode-mcp": {redirects: map[string]string{
		"http://localhost:33418/callback":         "an app on this computer (localhost:33418)",
		"http://127.0.0.1:33418/callback":         "an app on this computer (localhost:33418)",
		"https://claude.ai/api/mcp/auth_callback": "the Worklode connector in claude.ai",
	}},
}

// clientRedirect reports the consent destination for a registered client and
// an exactly matching redirect URI.
func clientRedirect(clientID, redirectURI string) (destination string, ok bool) {
	destination, ok = mcpClients[clientID].redirects[redirectURI]
	return destination, ok
}

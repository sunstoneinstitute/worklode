package hookrun

import (
	"encoding/json"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// handleOTelHeaders answers `lode-hook otel-headers`: Claude Code's
// otelHeadersHelper, run before every OTel export to supply the bearer token
// for the log exporter (spec 071 sec-5). It always exits 0 and writes exactly
// one JSON object to stdout -- {} when no token is found, so a missing
// credential never stops the agent, just leaves that export unauthenticated.
// The token never lands in a settings file; this is how Claude Code gets it
// instead.
//
// Token resolution mirrors defaultClient: cli.LoadConfig() checks LODE_TOKEN
// first, then the keychain/file token store for the resolved server, so this
// helper answers the same way `lode` itself would.
func handleOTelHeaders(opts Options) {
	headers := map[string]string{}
	cfg, err := cli.LoadConfig()
	if err != nil {
		warn(opts, "otel-headers: load config: %v", err)
	} else if cfg.Token != "" {
		headers["Authorization"] = "Bearer " + cfg.Token
	}
	body, err := json.Marshal(headers)
	if err != nil {
		body = []byte("{}") // headers is a map[string]string; Marshal cannot fail
	}
	fmt.Fprintln(opts.Stdout, string(body))
}

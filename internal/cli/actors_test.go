package cli_test

import (
	"context"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestClientActorsAndTokens(t *testing.T) {
	st, c, baseURL := newTestServer(t)
	ctx := context.Background()

	a, _, err := c.CreateActor(ctx, model.CreateActorInput{ID: "bob", Kind: "agent", DisplayName: "Bob"})
	if err != nil {
		t.Fatalf("CreateActor: %v", err)
	}
	if a.ID != "bob" || a.Kind != "agent" {
		t.Fatalf("CreateActor result = %+v", a)
	}

	tok, _, err := c.CreateToken(ctx, "bob", "bob's token", nil)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if !strings.HasPrefix(tok.Token, "wl_") {
		t.Fatalf("token = %q, want wl_ prefix", tok.Token)
	}
	// The freshly minted token actually authenticates.
	bobClient := cli.NewClient(cli.Config{ServerURL: baseURL, Token: tok.Token})
	if _, _, err := bobClient.ListTasks(ctx, cli.TaskListFilter{}); err != nil {
		t.Fatalf("list tasks as bob: %v", err)
	}

	// Revoke a token minted directly via the store, exercising the client's
	// revoke path independent of its own create path.
	tok2, err := st.CreateToken(ctx, "bob", "second token", nil)
	if err != nil {
		t.Fatalf("store.CreateToken: %v", err)
	}
	if _, err := c.RevokeToken(ctx, tok2); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	revokedClient := cli.NewClient(cli.Config{ServerURL: baseURL, Token: tok2})
	if _, _, err := revokedClient.ListTasks(ctx, cli.TaskListFilter{}); err == nil {
		t.Fatalf("list tasks with revoked token succeeded, want error")
	}
}

func TestClientWhoAmI(t *testing.T) {
	_, c, _ := newTestServer(t)

	who, _, err := c.WhoAmI(context.Background())
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if who.ID != "alice" || who.Kind != "human" || !who.Admin {
		t.Fatalf("WhoAmI = %+v; want the bootstrap admin actor alice", who)
	}
}

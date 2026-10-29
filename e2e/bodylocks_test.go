//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestBodyLockJourney drives the body editor's lock and conflict path over
// /api/v1 with two human tokens and an agent token (WL-REQ-1868): a held
// lock refuses a second take, the owner's unlock ends the holder's session,
// and an agent edit that bypasses the lock turns the next editor save into a
// conflict that carries the agent's text.
func TestBodyLockJourney(t *testing.T) {
	ctx := context.Background()
	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{BootstrapToken: bootstrapToken})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{ID: "locks", Name: "Locks", Key: "LK"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	clients := map[string]*cli.Client{}
	for id, kind := range map[string]string{"alice": "human", "bob": "human", "owner": "human", "agent": "agent"} {
		if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{ID: id, Kind: kind, DisplayName: id}); err != nil {
			t.Fatalf("create actor %s: %v", id, err)
		}
		tok, _, err := admin.CreateToken(ctx, id, "e2e body locks", nil)
		if err != nil {
			t.Fatalf("create token for %s: %v", id, err)
		}
		clients[id] = cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tok.Token})
	}
	alice, bob, agent := clients["alice"], clients["bob"], clients["agent"]

	rule, _, err := admin.AddRule(ctx, model.AddRuleInput{Project: "locks", Heading: "Locked rule", Body: "Original text."})
	if err != nil {
		t.Fatalf("add rule: %v", err)
	}
	owner := "owner"
	if _, _, err := admin.SetRuleMeta(ctx, rule.Ref, model.RuleMetaInput{Owner: &owner}); err != nil {
		t.Fatalf("set owner: %v", err)
	}
	id := strconv.FormatInt(rule.ID, 10)

	// conflict decodes a 409 into v.
	conflict := func(err error, v any) {
		t.Helper()
		var ce *cli.ClientError
		if !errors.As(err, &ce) || ce.Status != 409 {
			t.Fatalf("err = %v, want a 409", err)
		}
		if err := json.Unmarshal([]byte(ce.Msg), v); err != nil {
			t.Fatalf("decode 409 body %q: %v", ce.Msg, err)
		}
	}
	checkIn := func(c *cli.Client, session string) error {
		_, _, err := c.CheckInLock(ctx, "rule", id, model.LockCheckIn{Session: session, LastActivity: time.Now()})
		return err
	}

	// 1. alice takes the lock and checks in.
	aliceLock, _, err := alice.TakeLock(ctx, "rule", id)
	if err != nil || aliceLock.Holder != "alice" || aliceLock.Session == "" {
		t.Fatalf("alice take = %+v, %v; want holder alice with a session", aliceLock, err)
	}
	if err := checkIn(alice, aliceLock.Session); err != nil {
		t.Fatalf("alice check-in: %v", err)
	}

	// 2. bob's take is refused with alice as holder and no unlock right.
	_, _, err = bob.TakeLock(ctx, "rule", id)
	var held model.BodyLock
	conflict(err, &held)
	if held.Holder != "alice" || held.CanUnlock {
		t.Fatalf("bob take 409 = %+v, want holder alice, can_unlock false", held)
	}

	// 3. The owner unlocks; alice's check-in and save learn of it.
	if err := clients["owner"].UnlockBody(ctx, "rule", id); err != nil {
		t.Fatalf("owner unlock: %v", err)
	}
	var lost model.LockLost
	conflict(checkIn(alice, aliceLock.Session), &lost)
	if lost.By != "owner" {
		t.Fatalf("alice check-in 409 = %+v, want by owner", lost)
	}
	_, _, err = alice.EditRule(ctx, rule.Ref, model.EditRuleInput{Heading: "Locked rule", Body: "Alice text.", LockSession: aliceLock.Session})
	var lost2 model.LockLost
	conflict(err, &lost2)
	if lost2.Lock.Holder != "" {
		t.Fatalf("alice save 409 = %+v, want the free lock status", lost2)
	}

	// 4. bob takes the lock and loads the rule as blocks.
	bobLock, _, err := bob.TakeLock(ctx, "rule", id)
	if err != nil || bobLock.Holder != "bob" {
		t.Fatalf("bob take = %+v, %v", bobLock, err)
	}
	loaded, _, err := bob.GetRuleBlocks(ctx, rule.Ref)
	if err != nil || loaded.BodySHA256 == "" || len(loaded.Blocks) == 0 {
		t.Fatalf("bob load = sha %q blocks %d, %v", loaded.BodySHA256, len(loaded.Blocks), err)
	}

	// 5. The agent edits with no session and no check, bypassing bob's lock.
	if _, _, err := agent.EditRule(ctx, rule.Ref, model.EditRuleInput{Heading: "Locked rule", Body: "Agent text."}); err != nil {
		t.Fatalf("agent edit: %v", err)
	}

	// 6. bob's save with the hash he loaded conflicts and carries the agent's text.
	_, _, err = bob.EditRule(ctx, rule.Ref, model.EditRuleInput{
		Heading: "Locked rule", Body: "Bob text.", IfBodySHA256: loaded.BodySHA256, LockSession: bobLock.Session,
	})
	var bc model.BodyConflict
	conflict(err, &bc)
	if !strings.Contains(string(bc.Body), "Agent text.") || len(bc.Blocks) == 0 || bc.BodySHA256 == "" || bc.BodySHA256 == loaded.BodySHA256 {
		t.Fatalf("bob save 409 = %+v, want the agent's text and a new hash", bc)
	}

	// 7. bob re-saves against the conflict's hash.
	if _, _, err := bob.EditRule(ctx, rule.Ref, model.EditRuleInput{
		Heading: "Locked rule", Body: "Bob text.", IfBodySHA256: bc.BodySHA256, LockSession: bobLock.Session,
	}); err != nil {
		t.Fatalf("bob re-save: %v", err)
	}

	// 8. bob releases; the lock reads free.
	if err := bob.ReleaseLock(ctx, "rule", id, bobLock.Session); err != nil {
		t.Fatalf("bob release: %v", err)
	}
	free, _, err := bob.GetLock(ctx, "rule", id)
	if err != nil || free.Holder != "" {
		t.Fatalf("lock after release = %+v, %v; want no holder", free, err)
	}
}

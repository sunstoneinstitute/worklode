package cli_test

import (
	"context"
	"errors"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestClientSkillsList(t *testing.T) {
	st, c, _ := newTestServer(t)
	seedSkill(t, st, "tdd", "Red-green-refactor discipline")
	seedSkill(t, st, "debugging", "Systematic debugging loop")

	skills, raw, err := c.Skills(context.Background())
	if err != nil {
		t.Fatalf("Skills: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("Skills = %+v, want 2 entries", skills)
	}
	if len(raw) == 0 {
		t.Fatal("Skills: raw body empty")
	}
	names := map[string]model.Skill{}
	for _, sk := range skills {
		names[sk.Name] = sk
	}
	if names["acme:tdd"].Hash != "h-tdd" || names["acme:tdd"].Description != "Red-green-refactor discipline" {
		t.Fatalf("Skills[tdd] = %+v", names["acme:tdd"])
	}
}

func TestClientSkillGet(t *testing.T) {
	st, c, _ := newTestServer(t)
	seedSkill(t, st, "tdd", "Red-green-refactor discipline")

	sk, raw, err := c.Skill(context.Background(), "tdd")
	if err != nil {
		t.Fatalf("Skill: %v", err)
	}
	if sk.Name != "acme:tdd" || sk.Hash != "h-tdd" {
		t.Fatalf("Skill = %+v", sk)
	}
	if len(raw) == 0 {
		t.Fatal("Skill: raw body empty")
	}

	if _, _, err := c.Skill(context.Background(), "nope"); err == nil {
		t.Fatal("Skill(nope): want error, got nil")
	}
}

func TestClientSkillArchive(t *testing.T) {
	st, c, _ := newTestServer(t)
	seedSkill(t, st, "tdd", "Red-green-refactor discipline")

	data, err := c.SkillArchive(context.Background(), "tdd", "h-tdd")
	if err != nil {
		t.Fatalf("SkillArchive: %v", err)
	}
	if string(data) != "gzip-archive-tdd" {
		t.Fatalf("SkillArchive = %q, want %q", data, "gzip-archive-tdd")
	}

	if _, err := c.SkillArchive(context.Background(), "tdd", "wrong-hash"); err == nil {
		t.Fatal("SkillArchive(wrong hash): want error, got nil")
	}
}

func TestClientRecommendSkills(t *testing.T) {
	st, c, _ := newTestServer(t)
	seedSkill(t, st, "tdd", "Red-green-refactor discipline")

	rec, raw, err := c.RecommendSkills(context.Background(), "", "write tests first", 5)
	if err != nil {
		t.Fatalf("RecommendSkills: %v", err)
	}
	if rec.Provider != "none" {
		t.Fatalf("RecommendSkills.Provider = %q, want none", rec.Provider)
	}
	if len(raw) == 0 {
		t.Fatal("RecommendSkills: raw body empty")
	}

	// Neither task nor text: the server 422s, surfaced as a ClientError.
	if _, _, err := c.RecommendSkills(context.Background(), "", "", 5); err == nil {
		t.Fatal("RecommendSkills(neither): want error, got nil")
	}
}

func TestClientSyncSkills(t *testing.T) {
	_, c, _ := newTestServer(t)

	// alice (the newTestServer token) is an admin, but api.Config{} has no
	// skill sources configured, so this exercises the path/method wiring
	// and surfaces the server's own 422 rather than a decode error.
	_, _, err := c.SyncSkills(context.Background())
	if err == nil {
		t.Fatal("SyncSkills with no sources configured: want error, got nil")
	}
	var clientErr *cli.ClientError
	if !errors.As(err, &clientErr) || clientErr.Status != 422 {
		t.Fatalf("SyncSkills error = %v, want *cli.ClientError with status 422", err)
	}
}

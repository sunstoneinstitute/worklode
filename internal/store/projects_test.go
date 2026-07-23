package store

import (
	"errors"
	"reflect"
	"sort"
	"testing"
)

func TestCreateAndGetProject(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "horndb", "HornDB"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	got, err := s.GetProject(ctx, "horndb")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	want := &Project{ID: "horndb", Name: "HornDB", DeployGated: false}
	if *got != *want {
		t.Fatalf("GetProject: got %+v, want %+v", got, want)
	}
}

func TestGetProjectNotFound(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	_, err := s.GetProject(ctx, "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProject: want ErrNotFound, got %v", err)
	}
}

func TestListProjects(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "horndb", "HornDB"); err != nil {
		t.Fatalf("CreateProject horndb: %v", err)
	}
	if err := s.CreateProject(ctx, "worklode", "Work Tracker"); err != nil {
		t.Fatalf("CreateProject worklode: %v", err)
	}

	got, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListProjects: got %d projects, want 2", len(got))
	}
	ids := []string{got[0].ID, got[1].ID}
	sort.Strings(ids)
	if !reflect.DeepEqual(ids, []string{"horndb", "worklode"}) {
		t.Fatalf("ListProjects ids: got %v", ids)
	}
}

func TestSetDeployGated(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "horndb", "HornDB"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if err := s.SetDeployGated(ctx, "horndb", true); err != nil {
		t.Fatalf("SetDeployGated: %v", err)
	}

	got, err := s.GetProject(ctx, "horndb")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if !got.DeployGated {
		t.Fatalf("GetProject: want DeployGated=true after SetDeployGated")
	}

	if err := s.SetDeployGated(ctx, "horndb", false); err != nil {
		t.Fatalf("SetDeployGated false: %v", err)
	}
	got, err = s.GetProject(ctx, "horndb")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.DeployGated {
		t.Fatalf("GetProject: want DeployGated=false after unsetting")
	}
}

func TestAddRepoAndProjectForRepo(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "horndb", "HornDB"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.AddRepo(ctx, "horndb", "sunstoneinstitute/horndb"); err != nil {
		t.Fatalf("AddRepo: %v", err)
	}

	got, err := s.ProjectForRepo(ctx, "sunstoneinstitute/horndb")
	if err != nil {
		t.Fatalf("ProjectForRepo: %v", err)
	}
	if got.ID != "horndb" {
		t.Fatalf("ProjectForRepo: got project %q, want horndb", got.ID)
	}
}

func TestProjectForRepoUnmapped(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	_, err := s.ProjectForRepo(ctx, "sunstoneinstitute/nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ProjectForRepo: want ErrNotFound, got %v", err)
	}
}

func TestAddRepoDuplicateSameProject(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "horndb", "HornDB"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.AddRepo(ctx, "horndb", "sunstoneinstitute/horndb"); err != nil {
		t.Fatalf("AddRepo first: %v", err)
	}

	err := s.AddRepo(ctx, "horndb", "sunstoneinstitute/horndb")
	if !errors.Is(err, ErrRepoTaken) {
		t.Fatalf("AddRepo duplicate (same project): want ErrRepoTaken, got %v", err)
	}
}

func TestAddRepoDuplicateDifferentProject(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "horndb", "HornDB"); err != nil {
		t.Fatalf("CreateProject horndb: %v", err)
	}
	if err := s.CreateProject(ctx, "other", "Other"); err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	if err := s.AddRepo(ctx, "horndb", "sunstoneinstitute/horndb"); err != nil {
		t.Fatalf("AddRepo: %v", err)
	}

	err := s.AddRepo(ctx, "other", "sunstoneinstitute/horndb")
	if !errors.Is(err, ErrRepoTaken) {
		t.Fatalf("AddRepo duplicate (different project): want ErrRepoTaken, got %v", err)
	}
}

func TestListRepos(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "horndb", "HornDB"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.AddRepo(ctx, "horndb", "sunstoneinstitute/horndb"); err != nil {
		t.Fatalf("AddRepo horndb: %v", err)
	}
	if err := s.AddRepo(ctx, "horndb", "sunstoneinstitute/horndb-docs"); err != nil {
		t.Fatalf("AddRepo horndb-docs: %v", err)
	}

	got, err := s.ListRepos(ctx, "horndb")
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	sort.Strings(got)
	want := []string{"sunstoneinstitute/horndb", "sunstoneinstitute/horndb-docs"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListRepos: got %v, want %v", got, want)
	}
}

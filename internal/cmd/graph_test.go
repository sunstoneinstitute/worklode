package cmd

import (
	"reflect"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestTaskListDetailEdges(t *testing.T) {
	td := model.TaskListDetail{}
	td.ID = "WL-42"
	td.Edges.Out = []model.TaskEdgeOut{
		{To: "WL-1", Type: "child_of"},
		{To: "WL-99", Type: "blocks"},
	}
	td.Edges.In = []model.TaskEdgeIn{
		{From: "WL-3", Type: "blocks"},
	}

	out, in := taskListDetailEdges(td)

	wantOut := []model.Edge{
		{From: "WL-42", To: "WL-1", Type: "child_of"},
		{From: "WL-42", To: "WL-99", Type: "blocks"},
	}
	if !reflect.DeepEqual(out, wantOut) {
		t.Errorf("out = %+v, want %+v", out, wantOut)
	}

	wantIn := []model.Edge{
		{From: "WL-3", To: "WL-42", Type: "blocks"},
	}
	if !reflect.DeepEqual(in, wantIn) {
		t.Errorf("in = %+v, want %+v", in, wantIn)
	}
}

func TestTaskListDetailEdgesEmpty(t *testing.T) {
	td := model.TaskListDetail{}
	td.ID = "WL-1"

	out, in := taskListDetailEdges(td)
	if len(out) != 0 || len(in) != 0 {
		t.Errorf("out = %+v, in = %+v, want both empty", out, in)
	}
}

package cmd

import (
	"errors"
	"testing"
)

func TestExitCodeRejectsNonChildError(t *testing.T) {
	if _, ok := ExitCode(errors.New("not a child")); ok {
		t.Fatal("non-child error recognized")
	}
}

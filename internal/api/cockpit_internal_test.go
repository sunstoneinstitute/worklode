package api

import "testing"

func TestSelectMode(t *testing.T) {
	tests := []struct {
		name string
		in   modeFacts
		want cockpitMode
	}{
		{"candidate", modeFacts{IntakeCandidate: true}, modeEditorialDecision},
		{"promoted launch", modeFacts{PromotedFromIntake: true}, modeApprovedLaunch},
		{"entered research", modeFacts{PromotedFromIntake: true, EnteredResearch: true}, modeOperations},
		{"ordinary project", modeFacts{}, modeOperations},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectMode(tt.in); got != tt.want {
				t.Fatalf("selectMode(%+v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

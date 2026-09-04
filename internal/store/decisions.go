package store

import (
	"fmt"
	"regexp"
	"slices"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// decisionKeyRe is the shape a Decision.Key must take: lowercase letters,
// digits and hyphens.
var decisionKeyRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// decisionResponseTypes are the six response types §10.1 defines.
var decisionResponseTypes = []string{
	"single_select",
	"multi_select",
	"single_select_notes",
	"pick_or_freetext",
	"yes_no",
	"freetext",
}

// decisionTypesWithOptions are the response types that pose a set of
// options; yes_no and freetext answer without one.
var decisionTypesWithOptions = []string{
	"single_select",
	"multi_select",
	"single_select_notes",
	"pick_or_freetext",
}

// ValidateDecisionSpec checks the shape of a decision as it is posed (025
// §10.1): a routable key, a response_type the store recognizes with the
// options it needs and none it doesn't, and no answer yet. It does not
// check that the key is unique within the task — the database's UNIQUE
// constraint on (task, key) is the backstop for that.
func ValidateDecisionSpec(d model.Decision) error {
	if d.Key == "" {
		return fmt.Errorf("decision: key is required: %w", ErrInvalidInput)
	}
	if !decisionKeyRe.MatchString(d.Key) {
		return fmt.Errorf("decision %q: key must match [a-z0-9-]+: %w", d.Key, ErrInvalidInput)
	}
	if d.Question == "" {
		return fmt.Errorf("decision %q: question is required: %w", d.Key, ErrInvalidInput)
	}
	if !slices.Contains(decisionResponseTypes, d.ResponseType) {
		return fmt.Errorf("decision %q: response_type %q is not one of %v: %w",
			d.Key, d.ResponseType, decisionResponseTypes, ErrInvalidInput)
	}

	wantsOptions := slices.Contains(decisionTypesWithOptions, d.ResponseType)
	switch {
	case wantsOptions && len(d.Options) == 0:
		return fmt.Errorf("decision %q: options is required for %s: %w", d.Key, d.ResponseType, ErrInvalidInput)
	case !wantsOptions && len(d.Options) != 0:
		return fmt.Errorf("decision %q: options is not used by %s: %w", d.Key, d.ResponseType, ErrInvalidInput)
	}

	seenLabels := make(map[string]bool, len(d.Options))
	for _, opt := range d.Options {
		if opt.Label == "" {
			return fmt.Errorf("decision %q: an option has no label: %w", d.Key, ErrInvalidInput)
		}
		if seenLabels[opt.Label] {
			return fmt.Errorf("decision %q: option label %q is declared twice: %w", d.Key, opt.Label, ErrInvalidInput)
		}
		seenLabels[opt.Label] = true
	}

	if d.ResponseType != "multi_select" {
		if d.MinPicks != nil {
			return fmt.Errorf("decision %q: min_picks is only used by multi_select: %w", d.Key, ErrInvalidInput)
		}
		if d.MaxPicks != nil {
			return fmt.Errorf("decision %q: max_picks is only used by multi_select: %w", d.Key, ErrInvalidInput)
		}
	} else {
		if d.MinPicks != nil && *d.MinPicks < 1 {
			return fmt.Errorf("decision %q: min_picks must be at least 1: %w", d.Key, ErrInvalidInput)
		}
		if d.MaxPicks != nil {
			if *d.MaxPicks < 1 {
				return fmt.Errorf("decision %q: max_picks must be at least 1: %w", d.Key, ErrInvalidInput)
			}
			if *d.MaxPicks > len(d.Options) {
				return fmt.Errorf("decision %q: max_picks must be at most len(options): %w", d.Key, ErrInvalidInput)
			}
		}
		if d.MinPicks != nil && d.MaxPicks != nil && *d.MinPicks > *d.MaxPicks {
			return fmt.Errorf("decision %q: min_picks must be at most max_picks: %w", d.Key, ErrInvalidInput)
		}
	}

	if d.Answer != nil {
		return fmt.Errorf("decision %q: answer must be absent when posing: %w", d.Key, ErrInvalidInput)
	}
	if d.DecidedAt != nil {
		return fmt.Errorf("decision %q: decided_at must be absent when posing: %w", d.Key, ErrInvalidInput)
	}

	return nil
}

// validateAnswer checks a submitted answer against the decision's spec
// (025 §10.1): the fields its response_type uses hold values that satisfy
// its rule, and every field it doesn't use is empty. An answer that
// smuggles an unused field is refused, so what is stored is exactly what
// the type defines.
func validateAnswer(d model.Decision, a model.DecisionAnswer) error {
	switch d.ResponseType {
	case "single_select":
		if err := requireEmptyAnswerFields(d, a, "notes", "freetext", "value"); err != nil {
			return err
		}
		return requireSinglePick(d, a.Picked)

	case "multi_select":
		if err := requireEmptyAnswerFields(d, a, "notes", "freetext", "value"); err != nil {
			return err
		}
		return requireMultiPick(d, a.Picked)

	case "single_select_notes":
		if err := requireEmptyAnswerFields(d, a, "freetext", "value"); err != nil {
			return err
		}
		if a.Notes == "" {
			return fmt.Errorf("decision %q: notes is required for single_select_notes: %w", d.Key, ErrInvalidInput)
		}
		return requireSinglePick(d, a.Picked)

	case "pick_or_freetext":
		if err := requireEmptyAnswerFields(d, a, "notes", "value"); err != nil {
			return err
		}
		hasPick := len(a.Picked) > 0
		hasFreetext := a.Freetext != ""
		if hasPick == hasFreetext {
			return fmt.Errorf("decision %q: exactly one of picked or freetext is required for pick_or_freetext: %w",
				d.Key, ErrInvalidInput)
		}
		if hasPick {
			return requireSinglePick(d, a.Picked)
		}
		return nil

	case "yes_no":
		if err := requireEmptyAnswerFields(d, a, "picked", "notes", "freetext"); err != nil {
			return err
		}
		if a.Value != "yes" && a.Value != "no" && a.Value != "unsure" {
			return fmt.Errorf("decision %q: value must be yes, no or unsure: %w", d.Key, ErrInvalidInput)
		}
		return nil

	case "freetext":
		if err := requireEmptyAnswerFields(d, a, "picked", "notes", "value"); err != nil {
			return err
		}
		if a.Freetext == "" {
			return fmt.Errorf("decision %q: freetext is required for freetext: %w", d.Key, ErrInvalidInput)
		}
		return nil

	default:
		return fmt.Errorf("decision %q: response_type %q is not one of %v: %w",
			d.Key, d.ResponseType, decisionResponseTypes, ErrInvalidInput)
	}
}

// requireEmptyAnswerFields refuses an answer that sets any of the named
// DecisionAnswer fields ("picked", "notes", "freetext", "value") — fields
// the calling response_type does not define.
func requireEmptyAnswerFields(d model.Decision, a model.DecisionAnswer, fields ...string) error {
	for _, f := range fields {
		var set bool
		switch f {
		case "picked":
			set = len(a.Picked) != 0
		case "notes":
			set = a.Notes != ""
		case "freetext":
			set = a.Freetext != ""
		case "value":
			set = a.Value != ""
		}
		if set {
			return fmt.Errorf("decision %q: %s is not used by %s: %w", d.Key, f, d.ResponseType, ErrInvalidInput)
		}
	}
	return nil
}

// requireSinglePick refuses an answer whose picked does not name exactly
// one of d's offered option labels.
func requireSinglePick(d model.Decision, picked []string) error {
	if len(picked) != 1 {
		return fmt.Errorf("decision %q: picked must name exactly one option: %w", d.Key, ErrInvalidInput)
	}
	if !hasOptionLabel(d.Options, picked[0]) {
		return fmt.Errorf("decision %q: picked %q is not an offered option: %w", d.Key, picked[0], ErrInvalidInput)
	}
	return nil
}

// requireMultiPick refuses an answer whose picked repeats a label, names
// one that isn't offered, or falls outside [min_picks, max_picks] (default
// 1 and len(options)).
func requireMultiPick(d model.Decision, picked []string) error {
	min := 1
	if d.MinPicks != nil {
		min = *d.MinPicks
	}
	max := len(d.Options)
	if d.MaxPicks != nil {
		max = *d.MaxPicks
	}
	seen := make(map[string]bool, len(picked))
	for _, p := range picked {
		if !hasOptionLabel(d.Options, p) {
			return fmt.Errorf("decision %q: picked %q is not an offered option: %w", d.Key, p, ErrInvalidInput)
		}
		if seen[p] {
			return fmt.Errorf("decision %q: picked %q is repeated: %w", d.Key, p, ErrInvalidInput)
		}
		seen[p] = true
	}
	if len(picked) < min || len(picked) > max {
		return fmt.Errorf("decision %q: picked must name between %d and %d options: %w", d.Key, min, max, ErrInvalidInput)
	}
	return nil
}

func hasOptionLabel(opts []model.DecisionOption, label string) bool {
	for _, o := range opts {
		if o.Label == label {
			return true
		}
	}
	return false
}

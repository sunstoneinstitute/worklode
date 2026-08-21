package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// A skill's registry identity is plugin-qualified — <plugin>:<name> — because
// one bare name is not org-unique: two plugins under a single
// plugins/*/skills/* source legitimately ship the same skill name, and
// whichever synced second used to lose to a UNIQUE (name) constraint
// (037 §4.1). The qualifier comes from the plugin manifest at sync time; this
// file is the read side, turning whatever a pin was written as back into a
// row.

// QualifiedName is the skill's registry identity, <plugin>:<name>. It is what
// pins should name, what the API routes on, and what a client stores the
// skill under locally.
// Every stored skill has a qualifier (UpsertSkill requires one and
// skills_qualifier_nonempty enforces it), so this is a plain join rather than
// a fallback to the bare name — a skill without one would collapse back to
// the identity the qualifier exists to replace.
func (sk Skill) QualifiedName() string {
	return sk.Qualifier + ":" + sk.Name
}

// SkillRefResolution is one skill reference resolved against the registry.
// Exactly one of Skill and Candidates is meaningful: Skill is the row the ref
// names, or Candidates lists the qualified names a bare ref could not be
// narrowed to. Both empty means the ref matched nothing.
type SkillRefResolution struct {
	Ref        string
	Skill      *Skill
	Candidates []string
}

func (r SkillRefResolution) ambiguousErr() error {
	return fmt.Errorf("skill %s matches %s: qualify it: %w",
		r.Ref, strings.Join(r.Candidates, ", "), ErrAmbiguousSkill)
}

// ResolveSkillRefs resolves each ref in order, deduped to first occurrence.
// A ref resolves by three rules, tried in order (037 §4.2):
//
//  1. the qualified name, <plugin>:<name> — exact and authoritative;
//  2. a bare name matching exactly one skill;
//  3. for a qualified ref whose plugin is not in the registry, the segment
//     after its first colon as a bare name — what lets a pin written against
//     a plugin the org never synced still find an equivalent skill.
//
// Rules 2 and 3 report the candidates rather than guessing when more than one
// live skill matches. Soft-deleted skills resolve (so a brief warns rather
// than losing the pin) but never make a live skill ambiguous.
func (s *Store) ResolveSkillRefs(ctx context.Context, refs []string) ([]SkillRefResolution, error) {
	refs = dedupeFirst(refs)
	if len(refs) == 0 {
		return nil, nil
	}

	// One query for every lookup key any rule can use: the refs themselves,
	// plus rule 3's suffixes.
	keys := append([]string(nil), refs...)
	for _, ref := range refs {
		if _, suffix, ok := strings.Cut(ref, ":"); ok {
			keys = append(keys, suffix)
		}
	}
	keysJSON, err := json.Marshal(dedupeFirst(keys))
	if err != nil {
		return nil, fmt.Errorf("resolve skill refs: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, skillSelect+`
		WHERE s.qualifier || ':' || s.name IN (SELECT jsonb_array_elements_text($1::jsonb))
		   OR s.name IN (SELECT jsonb_array_elements_text($1::jsonb))
		ORDER BY s.qualifier, s.name`, string(keysJSON))
	if err != nil {
		return nil, fmt.Errorf("resolve skill refs: %w", err)
	}
	cands, err := collectRows(rows, "resolve skill refs", byValue(scanSkill))
	if err != nil {
		return nil, err
	}

	byQualified := make(map[string]Skill, len(cands))
	byBare := make(map[string][]Skill, len(cands))
	for _, sk := range cands {
		byQualified[sk.QualifiedName()] = sk
		byBare[sk.Name] = append(byBare[sk.Name], sk)
	}

	out := make([]SkillRefResolution, 0, len(refs))
	for _, ref := range refs {
		out = append(out, s.resolveSkillRef(ref, byQualified, byBare))
	}
	return out, nil
}

func (s *Store) resolveSkillRef(ref string, byQualified map[string]Skill, byBare map[string][]Skill) SkillRefResolution {
	r := SkillRefResolution{Ref: ref}
	if sk, ok := byQualified[ref]; ok { // rule 1
		r.Skill = &sk
		return r
	}
	bare := []string{ref} // rule 2
	if _, suffix, ok := strings.Cut(ref, ":"); ok {
		bare = append(bare, suffix) // rule 3
	}
	for _, name := range bare {
		sk, cands, ok := pickOne(byBare[name])
		if ok {
			r.Skill = sk
			return r
		}
		if len(cands) > 0 {
			r.Candidates = cands
			s.metrics.skillNameAmbiguous()
			return r
		}
	}
	return r
}

// pickOne narrows same-named skills to the one a bare reference means. A
// single live skill wins; several live ones are ambiguous and are returned as
// candidates. With no live skill a soft-deleted one still resolves, so a pin
// to a skill dropped from its source repo warns instead of reading as a typo.
func pickOne(cands []Skill) (*Skill, []string, bool) {
	var live []Skill
	for _, sk := range cands {
		if !sk.Deleted {
			live = append(live, sk)
		}
	}
	switch {
	case len(live) == 1:
		return &live[0], nil, true
	case len(live) > 1:
		names := make([]string, 0, len(live))
		for _, sk := range live {
			names = append(names, sk.QualifiedName())
		}
		return nil, names, false
	case len(cands) > 0:
		return &cands[0], nil, true
	default:
		return nil, nil, false
	}
}

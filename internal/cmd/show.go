package cmd

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

// typedID matches 025 §14.3's <KEY>-<TYPE>-<n> grammar (generalized by 029
// §4 to plans, milestones and deliverables), with an optional #sec- fragment
// for the SPEC/ADR case. It is checked before taskID: a document reference
// must never parse as a task id (025 §14.3).
var typedID = regexp.MustCompile(`^([A-Z][A-Z0-9]{1,9})-([A-Z][A-Z0-9]*)-(\d+(?:-\d+)?)(#sec-[\w.\-]+)?$`)

// taskID matches a full task id ("WL-12"); bareTaskNumber (scope.go) covers
// the bare-number shorthand.
var taskID = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}-[0-9]+$`)

// targetKind is what `lode show <id>` classified its argument as.
type targetKind int

const (
	// targetTask: a bare task number or a full task id — dispatch to
	// runTaskShow.
	targetTask targetKind = iota
	// targetDoc: a SPEC or ADR shorthand, or any other doc-ref shape — a
	// path, a filename, a number form, a bare slug — dispatch to runDocShow,
	// whose resolveDocRef owns the full grammar.
	targetDoc
	// targetUnshowable: a PLAN, MILE, or DEL id — a real entity kind with no
	// show support yet (spec 029 §4).
	targetUnshowable
	// targetUnknownType: a typed id whose <TYPE> segment names no known kind.
	targetUnknownType
	// targetUnclassified: matches no known shape at all.
	targetUnclassified
)

// showTarget is classify's result: the kind, plus the <TYPE> token for
// targetUnshowable and targetUnknownType, where the error message needs it.
type showTarget struct {
	Kind targetKind
	Type string
}

// unshowableKindWords names the entity a PLAN/MILE/DEL id refers to, singular,
// for the "not showable yet" error.
var unshowableKindWords = map[string]string{
	"PLAN": "plan",
	"MILE": "milestone",
	"DEL":  "deliverable",
}

const notYetAnEntity = "spec 029 §4 defines them; the entities land with spec 029"

// unshowableReason is the parenthetical each "not showable yet" error ends
// with, keyed by the singular kind word. Plans and milestones do not exist
// yet; a deliverable does (spec 029 §3), and saying otherwise would send
// someone looking for a row that is already there — so its reason names the
// surfaces that do read it.
var unshowableReason = map[string]string{
	"plan":      notYetAnEntity,
	"milestone": notYetAnEntity,
	"deliverable": "the entity exists; only the project's Deliverables page and " +
		"GET /api/v1/projects/{id}/deliverables read it so far",
}

// classify decides what arg names, by grammar alone — no filesystem or
// network access, so it is table-testable without cobra or a server.
//
// The last arm routes every remaining doc-ref shape — a path, a filename, a
// number-plus-slug form, a bare slug — to resolveDocRef, which owns that
// grammar (026 §3); classify only recognizes the silhouette. A bare number
// is checked as a task first, so `lode show 45` stays task 45 — spec 45 is
// `--spec 45` or `WL-SPEC-45` (the grammar collision WL-129 records).
func classify(arg string) showTarget {
	if m := typedID.FindStringSubmatch(arg); m != nil {
		typ := m[2]
		switch typ {
		case "SPEC", "ADR":
			return showTarget{Kind: targetDoc}
		case "PLAN", "MILE", "DEL":
			return showTarget{Kind: targetUnshowable, Type: typ}
		default:
			return showTarget{Kind: targetUnknownType, Type: typ}
		}
	}
	if bareTaskNumber.MatchString(arg) || taskID.MatchString(arg) {
		return showTarget{Kind: targetTask}
	}
	// Shape-match with any #fragment stripped; the fragment's own grammar is
	// resolveDocRef's business.
	if base, _ := designdoc.SplitFragment(arg); looksLikePath(base) || docRefShape.MatchString(base) {
		return showTarget{Kind: targetDoc}
	}
	return showTarget{Kind: targetUnclassified}
}

// docRefShape is the silhouette of resolveDocRef's non-path, non-shorthand
// forms: a lowercase slug, optionally digit-led ("025-documents-in-the-
// backbone"). Bare digits never reach it — the task arm runs first.
var docRefShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// showKinds lists the valid --kind values, and the seven per-kind flags, in
// the order the brief's surface documents them.
var showKinds = []string{"task", "spec", "adr", "plan", "milestone", "project", "deliverable"}

// showOrdinalShape validates a kind flag's value against its ordinal shape:
// spec/adr/milestone/deliverable/task take a bare integer, plan additionally
// allows a second ordinal ("4-1"). project has no entry here — any non-empty
// string is a valid slug/id — and is checked separately.
var showOrdinalShape = map[string]*regexp.Regexp{
	"task":        bareTaskNumber,
	"spec":        bareTaskNumber,
	"adr":         bareTaskNumber,
	"milestone":   bareTaskNumber,
	"deliverable": bareTaskNumber,
	"plan":        regexp.MustCompile(`^\d+(-\d+)?$`),
}

func newShowCmd() *cobra.Command {
	var kind, taskFlag, specFlag, adrFlag, planFlag, milestoneFlag, projectFlag, deliverableFlag, section string
	var pager, inline bool
	cmd := &cobra.Command{
		Use:   "show [id]",
		Short: "Show any entity by id or kind flag: a task, a design doc, a project",
		Long: `Show any entity, in one of two forms:

  lode show <id>                    classify the id and dispatch (a task,
                                    a document, or an entity kind with no
                                    show support yet). A document is named
                                    by shorthand (WL-SPEC-25), slug
                                    (design-doc-queries), number-and-slug
                                    (025-documents-in-the-backbone), or
                                    corpus path/filename — the same refs
                                    the lode doc verbs resolve. A bare
                                    number is always a task; a document by
                                    bare number is --spec/--adr <n>.
  lode show --<kind> <ordinal>      name the kind and its bare ordinal
                                    directly, e.g. --spec 15, --task 12
  lode show --kind <K> <ordinal>    the generic form of the same thing

At most one of --task/--spec/--adr/--plan/--milestone/--project/
--deliverable and --kind may be given, and never together with a
positional id — the flag's value already is the id. --section (-s)
narrows a spec or ADR render to one section (and its subsections) by
anchor; -s 3 is shorthand for -s sec-3.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cleanupPager := withPager(cmd, pager)
			defer cleanupPager()

			flagValues := map[string]string{
				"task": taskFlag, "spec": specFlag, "adr": adrFlag, "plan": planFlag,
				"milestone": milestoneFlag, "project": projectFlag, "deliverable": deliverableFlag,
			}
			sectionSet := cmd.Flags().Changed("section")

			var changedKind, changedValue string
			nChanged := 0
			for _, k := range showKinds {
				if cmd.Flags().Changed(k) {
					nChanged++
					changedKind, changedValue = k, flagValues[k]
				}
			}
			kindSet := cmd.Flags().Changed("kind")
			if kindSet {
				nChanged++
			}
			if nChanged > 1 {
				return errors.New("pass only one kind flag")
			}

			switch {
			case kindSet:
				if !slices.Contains(showKinds, kind) {
					return fmt.Errorf("unknown kind %q; valid kinds: %s", kind, strings.Join(showKinds, ", "))
				}
				if len(args) != 1 {
					return fmt.Errorf("--kind %s requires exactly one positional argument (the ordinal or slug)", kind)
				}
				return dispatchShowKind(cmd, kind, args[0], section, sectionSet, inline)
			case changedKind != "":
				if len(args) != 0 {
					return fmt.Errorf("--%s and a positional id are mutually exclusive: the flag's value is the id", changedKind)
				}
				return dispatchShowKind(cmd, changedKind, changedValue, section, sectionSet, inline)
			default:
				if len(args) != 1 {
					return errors.New("show requires exactly one argument: a task id, a document id, or a kind flag (--task, --spec, --adr, --plan, --milestone, --project, --deliverable, --kind)")
				}
				return dispatchShowPositional(cmd, args[0], section, sectionSet, inline)
			}
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", "generic kind flag, paired with the ordinal/slug positional: "+strings.Join(showKinds, "|"))
	cmd.Flags().StringVar(&taskFlag, "task", "", "show a task by bare number (e.g. --task 12; equivalent to the bare number positional)")
	cmd.Flags().StringVar(&specFlag, "spec", "", "show a spec by number (e.g. --spec 15)")
	cmd.Flags().StringVar(&adrFlag, "adr", "", "show an ADR by number (e.g. --adr 7)")
	cmd.Flags().StringVar(&planFlag, "plan", "", "show a plan by ordinal (e.g. --plan 4-1); not showable yet (spec 029 §4)")
	cmd.Flags().StringVar(&milestoneFlag, "milestone", "", "show a milestone by number (e.g. --milestone 2); not showable yet (spec 029 §4)")
	cmd.Flags().StringVar(&projectFlag, "project", "", "show a project's detail by id (e.g. --project worklode)")
	cmd.Flags().StringVar(&deliverableFlag, "deliverable", "", "show a deliverable by number (e.g. --deliverable 3); not showable yet — see the project's Deliverables page")
	cmd.Flags().StringVarP(&section, "section", "s", "", "print only this section (spec/adr only), by anchor: sec-3, #sec-3, or just 3")
	cmd.Flags().BoolVarP(&pager, "pager", "p", false, pagerFlagUsage)
	cmd.Flags().BoolVar(&inline, "inline", false, "for a spec or ADR: fold every effective amendment and supersession into the section it acts on (026 §3.2); ignored for tasks and projects")
	return cmd
}

func init() {
	rootCmd.AddCommand(newShowCmd())
}

// ordinalShapeError reports a kind flag's value failing its ordinal shape:
// flag values are bare ordinals, never shorthands, so the fix is either a bare
// ordinal on the flag or the full id positionally — "--spec WL-SPEC-6" is told
// to pass either "--spec 15" or the id "WL-SPEC-6" positionally. When value
// carries no recoverable ordinal the positional half of that suggestion would
// dangle, so the shorter form is emitted instead.
func ordinalShapeError(kind, value string) error {
	if ex := exampleOrdinal(value); ex != "<n>" {
		return fmt.Errorf("--%s takes a bare ordinal, not an id; pass either --%s %s or the id %s positionally", kind, kind, ex, value)
	}
	return fmt.Errorf("--%s takes a bare ordinal (e.g. --%s 15); %q is not one", kind, kind, value)
}

// exampleOrdinal best-effort extracts the ordinal component from a rejected
// flag value, for a concrete suggestion ("--spec 15" rather than "--spec
// <n>"): value often is a full typed id (typedID's grammar) whose <n>
// segment is exactly what the flag wanted. Falls back to a placeholder when
// value carries no recognizable ordinal.
func exampleOrdinal(value string) string {
	if m := typedID.FindStringSubmatch(value); m != nil {
		return m[3]
	}
	return "<n>"
}

// dispatchShowKind routes a resolved (kind, value) pair — from a --<kind>
// flag or from --kind <K> plus its positional — to the same routines the
// typed-id path (dispatchShowPositional) uses.
func dispatchShowKind(cmd *cobra.Command, kind, value, section string, sectionSet, inline bool) error {
	if sectionSet && kind != "spec" && kind != "adr" {
		return errors.New("--section applies only to specs and ADRs")
	}
	// project has no shape entry: any non-empty string is a valid slug or id,
	// and its own arm checks that.
	if re, ok := showOrdinalShape[kind]; ok && !re.MatchString(value) {
		return ordinalShapeError(kind, value)
	}
	switch kind {
	case "task":
		return runTaskShow(cmd, value)
	case "spec", "adr":
		return runDocShowByOrdinal(cmd, kind, value, section, inline)
	case "plan", "milestone", "deliverable":
		return fmt.Errorf("%s %s is not showable yet (%s)", kind, value, unshowableReason[kind])
	case "project":
		if value == "" {
			return errors.New("--project needs a value")
		}
		return runProjectShow(cmd, value, defaultCostDays)
	default:
		return fmt.Errorf("unhandled kind %q", kind)
	}
}

// runDocShowByOrdinal resolves a --spec/--adr flag's bare ordinal to a doc
// ref and renders it through runDocShow. A flag can only ever mean this
// repo's own project, so there is no foreign-key tier to consider. When the
// project key is known (config project_key), the ref is built as the local
// <KEY>-SPEC-<n>/<KEY>-ADR-<n> shorthand, which resolveDocRef resolves
// through its form 3 (and so kind-checks there). When the key is unknown
// there is no shorthand to build, so the ref falls back to the bare number
// form (form 2) — legitimate for a flag, where the equivalent positional
// shorthand (WL-SPEC-6) would instead get 026 §4.2's tier-3 "unresolved"
// treatment for an unknown key. Either way expectedKind is passed through to
// runDocShow, which verifies it against the resolved document's kind — so a
// keyless --adr on a spec (or vice versa) still gets the KindMismatchError,
// not a silent wrong-kind render.
func runDocShowByOrdinal(cmd *cobra.Command, kind, value, section string, inline bool) error {
	cfg, err := cli.LoadConfig()
	if err != nil {
		return err
	}
	typ := "SPEC"
	if kind == "adr" {
		typ = "ADR"
	}
	ref := value
	if cfg.ProjectKey != "" {
		ref = fmt.Sprintf("%s-%s-%s", cfg.ProjectKey, typ, value)
	}
	return runDocShow(cmd, ref, section, typ, inline)
}

// dispatchShowPositional is the classify-and-dispatch path for a plain `lode
// show <id>` (no kind flags): unchanged from the original show.go behavior,
// plus the --section-applies-only-to-docs check the flag-routed path also
// enforces.
func dispatchShowPositional(cmd *cobra.Command, arg, section string, sectionSet, inline bool) error {
	t := classify(arg)
	if sectionSet && t.Kind != targetDoc {
		return errors.New("--section applies only to specs and ADRs")
	}
	switch t.Kind {
	case targetTask:
		return runTaskShow(cmd, arg)
	case targetDoc:
		return runDocShow(cmd, arg, section, "", inline)
	case targetUnshowable:
		word := unshowableKindWords[t.Type]
		return fmt.Errorf("%s is a %s id; %ss are not showable yet (%s)", arg, word, word, unshowableReason[word])
	case targetUnknownType:
		return fmt.Errorf(`unknown entity type %q in %s; known types: SPEC, ADR, PLAN, MILE, DEL (a task id has no type segment: WL-12)`, t.Type, arg)
	default:
		return fmt.Errorf("cannot tell what %s names; pass a task id (12, WL-12) or a document ref (WL-SPEC-25, a slug, a corpus path)", arg)
	}
}

// rally.go derives what "add this spec to the rally" means (WL-SPEC-66
// §3.5): the tasks that drive one spec to completion, split into the ones
// that already exist and the two kinds that have to be minted first. Pure,
// like the rest of this package — the caller does every write.
package progress

import "github.com/sunstoneinstitute/worklode/internal/model"

// PlanFacts is what the derivation needs about one plan that the derived
// progress model does not already carry: the open decision task asking
// whether to accept it, when one exists ("" = none). Keyed by plan ref.
type PlanFacts struct {
	DecisionTask string
}

// Members is one spec's rally membership. Execute names tasks that exist now
// and are added as they are; the other two fields name work that has to be
// minted first, and the minted task is what joins the rally.
type Members struct {
	// Execute is every task to add by id: the open tasks of the spec's
	// accepted plans, plus the spec's open planning task and any open accept
	// decision, which are members in their own right once they exist. Reusing
	// them is what makes a second add of the same spec a no-op (§3.5).
	Execute []string
	// NeedsPlanning is true when the spec has an unplanned section and no
	// open planning task — the caller mints one (§3.4's task) and adds it.
	NeedsPlanning bool
	// NeedsAccept names the draft plans with no open decision task about
	// them, by document id. The caller mints one decision task per plan.
	NeedsAccept []int64
}

// RallyMembers is §3.5's membership rule for one spec. Completion has three
// kinds of remaining act and each becomes one task: execute, plan, accept.
//
// A plan in no_record contributes nothing — its work is done and its record
// is §6.2's chore, not a rally's — and neither does a plan that is not
// accepted, apart from the accept prompt a draft one earns. Landed and
// abandoned tasks are already out: Derive never puts an abandoned task in a
// plan's strip, and a landed one is not remaining work.
func RallyMembers(spec model.ProgressSpec, plans map[string]PlanFacts) Members {
	var m Members

	for _, p := range spec.Plans {
		switch {
		case p.State == "draft":
			// §3.5 case 3: the rally holds the prompt to accept, never the
			// acceptance. An open decision task is that prompt already.
			if task := plans[p.Ref].DecisionTask; task != "" {
				m.Execute = append(m.Execute, task)
				continue
			}
			m.NeedsAccept = append(m.NeedsAccept, p.Doc)
		case p.Status != "accepted" || p.State == "no_record":
			continue
		default:
			// §3.5 case 1: every task of an accepted plan that is neither
			// landed nor abandoned.
			for _, t := range p.Tasks {
				if t.Class != "landed" {
					m.Execute = append(m.Execute, t.ID)
				}
			}
		}
	}

	// §3.5 case 2: a spec with an unplanned section owes a plan, and the
	// planning task is the member — the open one when there is one.
	for _, sec := range spec.Sections {
		if sec.State != "unplanned" {
			continue
		}
		if spec.PlanningTask != "" {
			m.Execute = append(m.Execute, spec.PlanningTask)
		} else {
			m.NeedsPlanning = true
		}
		break
	}

	return m
}

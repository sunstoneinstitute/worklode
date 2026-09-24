package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/eventbus"
	"github.com/sunstoneinstitute/worklode/internal/gate"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// specReconcilerSubscriber is the eventbus subscriber that turns a Spec:
// trailer into a governing link (12-spec-refactoring-design-tree.md S4, S18;
// 11-design-authority-gate.md §4).
const specReconcilerSubscriber = "spec-reconciler"

// reconcilerOutcomes is the bounded label set of worklode_spec_reconciler_total.
var reconcilerOutcomes = []string{"linked", "no_task", "no_trailer", "malformed", "none", "planned", "unknown_target", "already"}

// reconcilerMetrics counts what the subscriber did with each event. Nil-safe.
type reconcilerMetrics struct {
	outcomes *prometheus.CounterVec
}

func newReconcilerMetrics(reg prometheus.Registerer) *reconcilerMetrics {
	m := &reconcilerMetrics{outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_spec_reconciler_total",
		Help: "Spec: trailers seen by the spec-reconciler subscriber, by outcome.",
	}, []string{"outcome"})}
	for _, o := range reconcilerOutcomes {
		m.outcomes.WithLabelValues(o)
	}
	reg.MustRegister(m.outcomes)
	return m
}

func (m *reconcilerMetrics) Outcome(o string) {
	if m != nil {
		m.outcomes.WithLabelValues(o).Inc()
	}
}

// errReconcileSkip aborts the RecordEvent transaction without recording
// anything: the event was understood and deliberately not acted on.
var errReconcileSkip = errors.New("spec-reconciler: skip")

// handleSpecReconcile reads the GitHub pull_request.* and push events the
// hooks record, finds the task the branch names, and governs it by the
// rule the Spec: trailer cites when no plan governs it (S51). Section refs
// are accepted until the per-project switch lands (S52). The task.governed
// event's external id identifies the link the trailer asks for, so every
// later delivery carrying the same trailer for the same task collides with
// it and is counted as already governed.
func (s *server) handleSpecReconcile(ctx context.Context, ev store.Event) (eventbus.Outcome, error) {
	if ev.Source != "github" {
		return eventbus.OutcomeSuppressed, nil
	}
	// A repo with no project mapping is recorded with its type suffixed
	// ".ignored" (internal/hooks/github.go); left unguarded, its
	// "pull_request.opened.ignored" would still match the prefix check below
	// and could govern a real task if the unmapped repo's branch happened to
	// share its name.
	if strings.HasSuffix(ev.Type, ".ignored") {
		return eventbus.OutcomeSuppressed, nil
	}
	// Decoded into map[string]any rather than a named struct: GitHub's
	// payload is a foreign schema internal/model does not own, and
	// internal/model/modelrule_test.go (ADR 036 §2) holds internal/api to no
	// json-tagged struct at all — internal/hooks is where such shapes are
	// named, for the raw webhook delivery this event was recorded from.
	var payload map[string]any
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		return eventbus.OutcomeApplied, fmt.Errorf("spec-reconciler: event %d payload: %w", ev.ID, err)
	}
	var taskID string
	var texts []string
	switch {
	case strings.HasPrefix(ev.Type, "pull_request."):
		pr, _ := payload["pull_request"].(map[string]any)
		body, _ := pr["body"].(string)
		head, _ := pr["head"].(map[string]any)
		ref, _ := head["ref"].(string)
		taskID = store.TaskIDFromRef(ref)
		if taskID == "" {
			taskID = store.TaskIDFromBody(body)
		}
		texts = []string{body}
	case ev.Type == "push":
		ref, _ := payload["ref"].(string)
		taskID = store.TaskIDFromRef(strings.TrimPrefix(ref, "refs/heads/"))
		commits, _ := payload["commits"].([]any)
		// Newest first: the final commit's trailer wins (11 §4).
		for i := len(commits) - 1; i >= 0; i-- {
			c, ok := commits[i].(map[string]any)
			if !ok {
				continue
			}
			msg, _ := c["message"].(string)
			texts = append(texts, msg)
		}
	default:
		return eventbus.OutcomeSuppressed, nil
	}
	if taskID == "" {
		s.reconcilerMetrics.Outcome("no_task")
		return eventbus.OutcomeSuppressed, nil
	}
	decl, err := gate.Find(gate.DefaultTrailer, strings.Join(texts, "\n"))
	switch {
	case errors.Is(err, gate.ErrNoTrailer):
		s.reconcilerMetrics.Outcome("no_trailer")
		return eventbus.OutcomeSuppressed, nil
	case err != nil:
		s.log.Warn("spec-reconciler: malformed trailer", "event", ev.ID, "task", taskID, "err", err)
		s.reconcilerMetrics.Outcome("malformed")
		return eventbus.OutcomeSuppressed, nil
	case decl.None != "":
		s.reconcilerMetrics.Outcome("none")
		return eventbus.OutcomeSuppressed, nil
	}

	outcome := "linked"
	notePayload, _ := json.Marshal(map[string]any{"task": taskID, "rule": decl.String(), "source": "gate", "event": ev.ID})
	// events.source is "watcher": like doc-lifecycle's mints, this event is
	// the system inferring a link, not a raw webhook delivery (there is no
	// "gate" value in events_source_check). The link itself still records
	// source "gate" on task_governed_by below (migration 0087), which is
	// what distinguishes it from a plan-minted or a manually added one.
	// The external id identifies the link, so a pull request pushed to twenty
	// times leaves one task.governed row behind.
	externalID := fmt.Sprintf("spec-reconciler-%s-%s", taskID, decl.String())
	_, inserted, err := s.st.RecordEvent(ctx, watcherEventSource, externalID, "task.governed", notePayload,
		func(tx *sql.Tx, _ int64) error {
			planned, err := store.HasPlanGovernance(tx, taskID)
			if err != nil {
				return err
			}
			if planned {
				outcome = "planned"
				return errReconcileSkip
			}
			var ruleID int64
			if decl.Rule != nil {
				ruleID, err = store.RuleIDByRef(tx, decl.Rule.Key, decl.Rule.Number)
			} else {
				ruleID, err = store.RuleAtSection(tx, *decl.Section)
			}
			if err != nil {
				return err
			}
			return store.Govern(tx, taskID, ruleID, "gate", false)
		})
	switch {
	case errors.Is(err, errReconcileSkip):
		s.reconcilerMetrics.Outcome(outcome)
		return eventbus.OutcomeSuppressed, nil
	case errors.Is(err, store.ErrNotFound):
		s.log.Warn("spec-reconciler: trailer names nothing", "event", ev.ID, "task", taskID, "spec", decl.String(), "err", err)
		s.reconcilerMetrics.Outcome("unknown_target")
		return eventbus.OutcomeSuppressed, nil
	case err != nil:
		return eventbus.OutcomeError, fmt.Errorf("spec-reconciler: event %d: %w", ev.ID, err)
	case !inserted:
		s.reconcilerMetrics.Outcome("already")
		return eventbus.OutcomeSuppressed, nil
	}
	s.reconcilerMetrics.Outcome("linked")
	return eventbus.OutcomeApplied, nil
}

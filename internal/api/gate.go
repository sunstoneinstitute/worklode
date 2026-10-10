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
// trailer into a governing link (WL-REQ-1552).
const specReconcilerSubscriber = "spec-reconciler"

// reconcilerOutcomes is the bounded label set of worklode_spec_reconciler_total.
var reconcilerOutcomes = []string{"linked", "no_task", "no_trailer", "malformed", "section", "none", "planned", "unknown_target", "already"}

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
// rule the Spec: trailer cites when no plan governs it (WL-REQ-1552). A
// section value names no rule and is counted and logged. The task.governed
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
	// internal/model/modelrule_test.go (WL-RULE-1349) holds internal/api to no
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
		// Newest first: the final commit's trailer wins (WL-REQ-7).
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
	case errors.As(err, new(*gate.SectionError)):
		s.log.Warn("spec-reconciler: trailer names a section, not a rule", "event", ev.ID, "task", taskID, "err", err)
		s.reconcilerMetrics.Outcome("section")
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
	// source "gate" on task_governed_by below, which is what distinguishes
	// it from a plan-minted or a manually added one. The external id is
	// keyed on the resolved rule, so a pull request pushed to twenty times,
	// or a trailer that differs only by its qualifier, leaves one
	// task.governed row behind.
	var ruleID int64
	err = s.st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		ruleID, err = store.RuleIDByRef(tx, decl.Rule.Key, decl.Rule.Number)
		return err
	})
	if err == nil {
		externalID := fmt.Sprintf("spec-reconciler-%s-%d", taskID, ruleID)
		var inserted bool
		_, inserted, err = s.st.RecordEvent(ctx, watcherEventSource, externalID, "task.governed", notePayload,
			func(tx *sql.Tx, _ int64) error {
				planned, err := store.HasPlanGovernance(tx, taskID)
				if err != nil {
					return err
				}
				if planned {
					outcome = "planned"
					return errReconcileSkip
				}
				return store.Govern(tx, taskID, ruleID, "gate", false)
			})
		if err == nil && !inserted {
			outcome = "already"
		}
	}
	switch {
	case errors.Is(err, errReconcileSkip):
	case errors.Is(err, store.ErrNotFound):
		s.log.Warn("spec-reconciler: trailer names nothing", "event", ev.ID, "task", taskID, "spec", decl.String(), "err", err)
		outcome = "unknown_target"
	case err != nil:
		return eventbus.OutcomeError, fmt.Errorf("spec-reconciler: event %d: %w", ev.ID, err)
	}
	s.reconcilerMetrics.Outcome(outcome)
	if outcome != "linked" {
		return eventbus.OutcomeSuppressed, nil
	}
	return eventbus.OutcomeApplied, nil
}

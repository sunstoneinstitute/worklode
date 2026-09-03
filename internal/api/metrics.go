package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/derive"
	"github.com/sunstoneinstitute/worklode/internal/mdrender"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/ns"
	"github.com/sunstoneinstitute/worklode/internal/overview"
	"github.com/sunstoneinstitute/worklode/internal/skillsync"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// initMetrics creates and registers the server-owned instruments (HTTP
// middleware and skill sync) on reg.
func (s *server) initMetrics(reg prometheus.Registerer) {
	s.requests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "HTTP requests served, by method, route pattern, and status code.",
	}, []string{"method", "route", "code"})
	s.durations = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request duration, by method and route pattern.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})
	s.syncRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_skill_sync_runs_total",
		Help: "Skill sync passes by result.",
	}, []string{"result"})
	s.syncDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "worklode_skill_sync_duration_seconds",
		Help:    "Skill sync pass duration.",
		Buckets: []float64{0.1, 0.5, 1, 5, 15, 60, 300},
	})
	s.syncItems = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_skill_sync_items_total",
		Help: "Skills touched by sync passes, by action.",
	}, []string{"action"})
	s.assignments = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_task_assignments_total",
		Help: "Task assignment actions, by action (assign, unassign, start, stop).",
	}, []string{"action"})
	s.crewChanges = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_crew_changes_total",
		Help: "Project Crew membership changes, by surface (api, web), action (add, remove), and outcome (ok, rejected, error). Labels are bounded: the project, the actor and the role label are deliberately not among them.",
	}, []string{"surface", "action", "outcome"})
	s.milestoneChanges = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_milestone_changes_total",
		Help: "Milestone changes (spec 029 §2), by action (" + strings.Join(milestoneChangeActions, ", ") +
			") and outcome (" + strings.Join(milestoneChangeOutcomes, ", ") +
			"). Labels are bounded: the project, the milestone and the actor are deliberately not among them.",
	}, []string{"action", "outcome"})
	s.repoMappings = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_repo_mapping_changes_total",
		Help: "Project/repo mapping changes, by action (add, edit, remove) and outcome (ok, rejected, error). Labels are bounded: the repo and the project are deliberately not among them.",
	}, []string{"action", "outcome"})
	s.cockpitProjections = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_cockpit_projection_requests_total",
		Help: "Project cockpit projection assembly attempts, by surface (api, web) and outcome (ok, not_found, error).",
	}, []string{"surface", "outcome"})
	s.navigations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_web_navigation_requests_total",
		Help: "Web UI navigation requests, by destination and outcome (ok, not_found, error).",
	}, []string{"destination", "outcome"})
	s.homeRenders = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_web_home_renders_total",
		Help: "Home page renders, by mode (" + strings.Join(homeRenderModes, ", ") +
			"). A rising \"empty\" share means people are landing on a Home with nothing on it, and a stuck \"open\" share on an instance that has a login provider means requests are arriving unauthenticated. Bounded by construction: no project or task id is a label here.",
	}, []string{"mode"})
	s.runBoardRenders = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_web_run_board_renders_total",
		Help: "Project run board page renders (GET /projects/{id}/work, 032 §8), by outcome (" +
			strings.Join(runBoardRenderOutcomes, ", ") +
			"). A rising \"empty\" share means projects are landing on a run board with no live work. Bounded by construction: no project or task id is a label here.",
	}, []string{"outcome"})
	s.inboxRenders = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_web_inbox_renders_total",
		Help: "Cross-project inbox page renders (GET /inbox, 056 §3), by outcome (" +
			strings.Join(inboxRenderOutcomes, ", ") +
			"). A rising \"empty\" share means people are landing on an inbox with nothing waiting on them. Bounded by construction: no actor or task id is a label here.",
	}, []string{"outcome"})
	s.authzDecisions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_authz_decisions_total",
		Help: "Authorization decisions, by permission and outcome (allow, deny). A deny rate above zero on a permission nobody should be attempting is the signal worth alerting on.",
	}, []string{"permission", "outcome"})
	s.approvalDecisions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_approval_decisions_total",
		Help: "Decisions submitted to POST /approvals/{id}/decide (029 §7.3), by decision (" +
			strings.Join(approvalDecisionKinds, ", ") + ") and outcome (" +
			strings.Join(approvalDecisionOutcomes, ", ") +
			"). Labels are bounded: the approval, the decider and the required role are deliberately not among them. The session refusal in front of the route is counted by worklode_authz_decisions_total, not here.",
	}, []string{"decision", "outcome"})
	s.taskTokens = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_task_tokens_total",
		Help: "Task-scoped token mints (POST /tasks/{id}/tokens, 001 §2.1), by outcome (" +
			strings.Join(taskTokenOutcomes, ", ") + ").",
	}, []string{"outcome"})
	s.dictations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_dictations_total",
		Help: "Dictation requests (POST /dictate, WL-299), by outcome (" +
			strings.Join(dictationOutcomes, ", ") + "). The proxy call to the speech-to-text provider is the only outbound work; 'unconfigured' on a deployment with no provider is a stale page or a hand-built request, never the button, which is not rendered then.",
	}, []string{"outcome"})
	s.formSubmissions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_web_form_submissions_total",
		Help: "Web UI write-form submissions, by form (task, deliverable, crew_add, crew_remove) and outcome (created, invalid, forbidden, not_found, error); \"created\" is an accepted submission, which for crew_remove means the member was removed.",
	}, []string{"form", "outcome"})
	s.localMerges = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_local_merge_reports_total",
		Help: "Tasks named in a local merge report, by result (advanced, duplicate, unknown_task). Steady 'duplicate' traffic is what a healthy webhook-plus-clone pair looks like; its absence means a reporter has stopped.",
	}, []string{"result"})
	s.listExpansions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_list_expansions_total",
		Help: "List endpoint requests that asked for an expansion, by endpoint (tasks, docs) and expansion (detail, body, tree).",
	}, []string{"endpoint", "expansion"})
	s.blobUploads = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_blob_uploads_total",
		Help: "Blob uploads (POST /api/v1/blobs), by outcome (" +
			strings.Join(blobUploadOutcomes, ", ") +
			"). A rising 'deduplicated' share is healthy — it is the same screenshot being pasted twice, costing one query and no object write.",
	}, []string{"outcome"})
	s.blobServes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_blob_serves_total",
		Help: "Blob fetches (GET /blob/{hash}), by outcome (" +
			strings.Join(blobServeOutcomes, ", ") +
			"). Sustained 'not_found' means task bodies reference blobs the index has lost, which renders as broken images.",
	}, []string{"outcome"})
	s.posterExtractions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_video_poster_extractions_total",
		Help: "First-frame poster extractions attempted for uploaded videos, by outcome (" +
			strings.Join(posterExtractionOutcomes, ", ") +
			"). A solid line of 'unavailable' means this image shipped without ffmpeg and every embedded video renders as a black rectangle.",
	}, []string{"outcome"})
	s.taskBlobRefs = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_task_blob_refs_total",
		Help: "Explicit task blob references changed by the attach/detach endpoints, by action (" +
			strings.Join(taskBlobRefActions, ", ") +
			"). Only the declared half of the reference graph: embedded references follow the task body and are not counted here.",
	}, []string{"action"})
	s.blobGCRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_blob_gc_runs_total",
		Help: "GC sweeps (POST /api/v1/blobs/gc), by mode (" +
			strings.Join(blobGCModes, ", ") +
			") and outcome (" + strings.Join(blobGCOutcomes, ", ") +
			"). A dry_run rate near zero means operators are applying sweeps without previewing them first.",
	}, []string{"mode", "outcome"})
	s.blobGCObjects = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_blob_gc_objects_total",
		Help: "Blobs and objects a GC sweep found or acted on, by action (" +
			strings.Join(blobGCObjectActions, ", ") +
			"). A steady 'orphan' rate outside the upload path's expected partial-failure rate means something else is leaving objects with no index row.",
	}, []string{"action"})
	s.imageMirrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_image_mirrors_total",
		Help: "Remote images in a promoted issue body that mirroring tried to turn into blobs, by outcome (" +
			strings.Join(imageMirrorOutcomes, ", ") +
			"). Each remote reference contributes exactly one outcome. Anything but 'mirrored' or 'deduplicated' leaves an off-site URL in a task body, which renders as nothing.",
	}, []string{"outcome"})
	s.mirrorTokens = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_image_mirror_tokens_total",
		Help: "GitHub App installation tokens a mirroring pass minted for the images it was about to fetch, by outcome (" +
			strings.Join(mirrorTokenOutcomes, ", ") +
			"). One per promote that had remote images, not one per image. A 'failed' rate above zero means private-repo images are being fetched unauthenticated, which shows up on worklode_image_mirrors_total as fetch_failed.",
	}, []string{"outcome"})
	s.kindAliasUses = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_task_kind_alias_uses_total",
		Help: "Requests naming a deprecated task kind that was normalised to its current name, by alias and surface (" +
			strings.Join(kindAliasSurfaces, ", ") +
			"). Pre-initialised, so a flat zero means no request has used a retired spelling.",
	}, []string{"alias", "surface"})
	s.deletes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_deletes_total",
		Help: "Tombstone operations on tasks and design documents (044 §6), by entity (" +
			strings.Join(deleteEntities, ", ") + "), op (" +
			strings.Join(deleteOps, ", ") + ") and outcome (" +
			strings.Join(deleteOutcomes, ", ") + ").",
	}, []string{"entity", "op", "outcome"})
	// A distinct counter, not left to http_requests_total, because a seek is
	// the one admin-triggered write on this surface: it is the only way an
	// operator moves a subscriber's offsets backwards (025 §18), and how
	// often that happens is worth alerting on independently of request
	// volume. The GET reads beside it (listEvents, listEventSubscribers) are
	// ordinary reads with no derived outcome, so the generic HTTP middleware
	// (http_requests_total/http_request_duration_seconds) is judged
	// sufficient for them — see the eventbus package's
	// worklode_event_subscriber_lag and worklode_events_processed_total for
	// the domain-level visibility into the log itself.
	s.eventSubscriberSeeks = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_event_subscriber_seeks_total",
		Help: "Admin seeks of a subscriber's offsets (POST /api/v1/event-subscribers/{name}/seek), by subscriber.",
	}, []string{"subscriber"})
	// The stream's own two instruments. http_requests_total cannot stand in
	// for either: a follow is one request that lasts hours, so it is counted
	// once at the end and its duration lands in http_request_duration_seconds
	// only when the client hangs up. How many follows are open right now, and
	// how much they are pushing, are the two questions an operator actually
	// asks about this route.
	s.eventStreamsActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "worklode_event_streams_active",
		Help: "Open event-log follows (GET /api/v1/events/stream). Each holds a connection and a repeating horizon-bounded query.",
	})
	s.eventStreamEventsSent = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "worklode_event_stream_events_sent_total",
		Help: "Events pushed to event-log followers, summed across all open streams.",
	})
	// Spec 007's two families. http_requests_total cannot answer either
	// question: a 503 from a graph-less instance and a 500 from a broken
	// SPARQL endpoint are both "not 200", and one POST /api/v1/derive runs
	// two derivers whose outcomes differ independently of its status code.
	s.overviewReads = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_overview_reads_total",
		Help: "Spec 007 overview reads, by read (" +
			strings.Join(overviewReadKinds, ", ") + ") and outcome (" +
			strings.Join(overviewReadOutcomes, ", ") +
			"). Sustained no_graph means the instance is serving the surface with LODE_GRAPHSERVER_URL unset, so drift and gaps are answering nothing.",
	}, []string{"read", "outcome"})
	s.deriveRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_derive_runs_total",
		Help: "Server-side deriver runs (POST /api/v1/derive), by source (" +
			strings.Join(deriveSources, ", ") + ") and outcome (" +
			strings.Join(deriveOutcomes, ", ") +
			"). One observation per deriver, not per request. A steady 'skipped' share is healthy — it is the content hash matching, costing no write; 'refused_empty' is the guard against a broken input replacing a graph with nothing.",
	}, []string{"source", "outcome"})
	// The horizon's position is a scrape-time fact, not something a handler
	// increments, so it is a collector rather than a gauge. It lives here
	// because this is where it gets registered: eventbus.NewMetrics, which
	// owns the per-subscriber lag gauge, has no caller yet.
	if s.st != nil {
		reg.MustRegister(&eventHorizonCollector{horizonID: s.st.EventLogHorizonID})
	}
	// The task-body render cache owns its own instruments (WL-222), so it is
	// built here rather than in NewServer: this is where the registerer is.
	s.mdcache = mdrender.NewCache(reg)
	reg.MustRegister(s.requests, s.durations, s.syncRuns, s.syncDuration, s.syncItems, s.assignments,
		s.cockpitProjections, s.navigations, s.homeRenders, s.runBoardRenders, s.inboxRenders, s.formSubmissions, s.dictations, s.taskTokens, s.authzDecisions,
		s.approvalDecisions,
		s.crewChanges,
		s.milestoneChanges,
		s.repoMappings,
		s.localMerges,
		s.eventSubscriberSeeks, s.eventStreamsActive, s.eventStreamEventsSent, s.listExpansions,
		s.blobUploads, s.blobServes, s.posterExtractions, s.taskBlobRefs,
		s.blobGCRuns, s.blobGCObjects, s.imageMirrors, s.mirrorTokens,
		s.kindAliasUses, s.deletes,
		s.overviewReads, s.deriveRuns)

	// Pre-initialise so alert expressions see 0, not no-data (as serve.go does
	// for the sweeper). listExpansions is deliberately left out: an absent
	// series is how "no client has ever asked for an expansion" reads, and a
	// test asserts the series is absent before the first expanded request.
	s.syncRuns.WithLabelValues("ok")
	s.syncRuns.WithLabelValues("error")
	for _, action := range []string{"assign", "unassign", "start", "stop"} {
		s.assignments.WithLabelValues(action)
	}
	for _, result := range []string{
		store.LocalMergeAdvanced, store.LocalMergeDuplicate, store.LocalMergeUnknownTask,
	} {
		s.localMerges.WithLabelValues(result)
	}
	for _, surface := range []string{"api", "web"} {
		for _, outcome := range []string{"ok", "not_found", "error"} {
			s.cockpitProjections.WithLabelValues(surface, outcome)
		}
	}
	// Every declared permission, so a permission that is never exercised
	// reads as a flat zero rather than as no-data — the difference between
	// "nobody tried" and "we are not measuring".
	for perm := range grants {
		for _, outcome := range []string{"allow", "deny"} {
			s.authzDecisions.WithLabelValues(string(perm), outcome)
		}
	}
	for _, surface := range []string{"api", "web"} {
		for _, action := range crewChangeActions {
			for _, outcome := range crewChangeOutcomes {
				s.crewChanges.WithLabelValues(surface, action, outcome)
			}
		}
	}
	for _, action := range milestoneChangeActions {
		for _, outcome := range milestoneChangeOutcomes {
			s.milestoneChanges.WithLabelValues(action, outcome)
		}
	}
	for _, action := range repoMappingActions {
		for _, outcome := range repoMappingOutcomes {
			s.repoMappings.WithLabelValues(action, outcome)
		}
	}
	// All three Home modes, so "nobody has hit the empty state" reads as a
	// flat zero rather than as no-data.
	for _, mode := range homeRenderModes {
		s.homeRenders.WithLabelValues(mode)
	}
	// Both run board outcomes, so an instance with no live work anywhere
	// reads as a flat zero rather than as no-data.
	for _, outcome := range runBoardRenderOutcomes {
		s.runBoardRenders.WithLabelValues(outcome)
	}
	// Both inbox outcomes, so an instance where nobody has anything waiting
	// on them reads as a flat zero rather than as no-data.
	for _, outcome := range inboxRenderOutcomes {
		s.inboxRenders.WithLabelValues(outcome)
	}
	for _, form := range []string{"task", "deliverable", "crew_add", "crew_remove",
		formRestoreTask, formRestoreDoc} {
		for _, outcome := range []string{"created", "invalid", "forbidden", "not_found", "error"} {
			s.formSubmissions.WithLabelValues(form, outcome)
		}
	}
	for _, outcome := range dictationOutcomes {
		s.dictations.WithLabelValues(outcome)
	}
	for _, outcome := range taskTokenOutcomes {
		s.taskTokens.WithLabelValues(outcome)
	}
	// Every decision/outcome pair, so an instance where nobody has decided an
	// approval reads as a flat zero rather than as no-data — the difference
	// between "no decision was refused" and "refusals are not being counted".
	for _, decision := range approvalDecisionKinds {
		for _, outcome := range approvalDecisionOutcomes {
			s.approvalDecisions.WithLabelValues(decision, outcome)
		}
	}
	// Both blob families in full, so an instance with no bucket configured
	// reads as a flat zero across every outcome rather than as no-data.
	for _, outcome := range blobUploadOutcomes {
		s.blobUploads.WithLabelValues(outcome)
	}
	for _, outcome := range blobServeOutcomes {
		s.blobServes.WithLabelValues(outcome)
	}
	for _, outcome := range posterExtractionOutcomes {
		s.posterExtractions.WithLabelValues(outcome)
	}
	for _, action := range taskBlobRefActions {
		s.taskBlobRefs.WithLabelValues(action)
	}
	for _, mode := range blobGCModes {
		for _, outcome := range blobGCOutcomes {
			s.blobGCRuns.WithLabelValues(mode, outcome)
		}
	}
	for _, action := range blobGCObjectActions {
		s.blobGCObjects.WithLabelValues(action)
	}
	for _, outcome := range imageMirrorOutcomes {
		s.imageMirrors.WithLabelValues(outcome)
	}
	for _, outcome := range mirrorTokenOutcomes {
		s.mirrorTokens.WithLabelValues(outcome)
	}
	// Every reachable entity/op/outcome combination, so an instance where
	// nobody has deleted anything reads as a flat zero rather than as no-data
	// — the difference between "no delete was refused" and "refusals are not
	// being counted". Undelete asks for no justification on either instance
	// (044 §3), so justification_required is unreachable there and would be a
	// permanently flat series claiming to mean something.
	for _, entity := range deleteEntities {
		for _, op := range deleteOps {
			for _, outcome := range deleteOutcomes {
				if op == opUndelete && outcome == deleteJustificationRequired {
					continue
				}
				s.deletes.WithLabelValues(entity, op, outcome)
			}
		}
	}
	// Every reachable read/outcome pair, so an instance nobody has asked for
	// an overview on reads as a flat zero rather than as no-data. no_graph is
	// minted only for the two graph-backed reads: the other three are
	// computed from the backbone and cannot return ErrNoGraph, so the series
	// would be permanently flat while claiming to mean something.
	for _, read := range overviewReadKinds {
		for _, outcome := range overviewReadOutcomes {
			if outcome == overviewNoGraph && read != readDrift && read != readGaps {
				continue
			}
			s.overviewReads.WithLabelValues(read, outcome)
		}
	}
	// Both derivers in full, so an instance where nobody has run them reads
	// as a flat zero across every outcome rather than as no-data.
	for _, source := range deriveSources {
		for _, outcome := range deriveOutcomes {
			s.deriveRuns.WithLabelValues(source, outcome)
		}
	}
	// Every alias on every surface, because the whole point of this counter is
	// to prove nothing still sends the deprecated spelling before the alias is
	// dropped, and an absent series cannot prove that.
	for alias := range ns.DeprecatedTaskKinds {
		for _, surface := range kindAliasSurfaces {
			s.kindAliasUses.WithLabelValues(alias, surface)
		}
	}
}

// A follow that has gone quiet reads identically whether the log is quiet or
// the commit horizon is stuck behind a long-running transaction: the stream
// keeps heartbeating either way (025 §15). This gauge is the difference —
// flat while events are still being recorded means the horizon is held back,
// and pg_stat_activity is the next place to look.
var eventLogHorizonDesc = prometheus.NewDesc(
	"worklode_event_log_horizon_id",
	"Highest event id below the commit horizon (pg_snapshot_xmin) at scrape time. A log position, not a count.",
	nil, nil)

// eventHorizonCollector reads the horizon at scrape time, in the same mould as
// eventbus's lag collector: a bounded timeout, and an invalid metric on
// failure so a scrape error surfaces instead of a stale zero.
type eventHorizonCollector struct {
	horizonID func(context.Context) (int64, error)
}

func (c *eventHorizonCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- eventLogHorizonDesc
}

func (c *eventHorizonCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	id, err := c.horizonID(ctx)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(eventLogHorizonDesc, err)
		return
	}
	ch <- prometheus.MustNewConstMetric(eventLogHorizonDesc, prometheus.GaugeValue, float64(id))
}

// observeSkillSync records one sync pass, called from both syncOnce
// (background) and the admin sync handler. A partial failure still carries a
// summary of what landed before the error, so items are recorded on both
// paths (spec 022 §4).
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeSkillSync(sum skillsync.Summary, err error, d time.Duration) {
	if s.syncDuration == nil {
		return
	}
	s.syncDuration.Observe(d.Seconds())
	result := "ok"
	if err != nil {
		result = "error"
	}
	s.syncRuns.WithLabelValues(result).Inc()
	for action, n := range map[string]int{
		"synced":  sum.Synced,
		"changed": sum.Changed,
		"deleted": sum.Deleted,
	} {
		s.syncItems.WithLabelValues(action).Add(float64(n))
	}
}

// initNavMetrics pre-initialises the navigation series for every destination
// the registered routes wrap (see navWrap), so a page that has not been
// visited yet reports zero rather than being absent from the scrape. Called
// from NewServer once registerRoutes has returned — not from inside it, where
// a route registered below the call would silently lose its zero-series.
// Nil-safe like the observers, for tests that build a *server directly.
func (s *server) initNavMetrics() {
	if s.navigations == nil {
		return
	}
	for _, destination := range s.navDestinations {
		for _, outcome := range []string{"ok", "not_found", "error", "rejected"} {
			s.navigations.WithLabelValues(destination, outcome)
		}
	}
}

// observeAssignment records one assignment action (assign, unassign, start,
// stop). Called on success only.
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeAssignment(action string) {
	if s.assignments == nil {
		return
	}
	s.assignments.WithLabelValues(action).Inc()
}

// crewChangeActions are every action label worklode_crew_changes_total
// carries: the two membership mutations spec 029 §6.1 defines.
var crewChangeActions = []string{"add", "remove"}

// crewChangeOutcomes are every outcome label worklode_crew_changes_total
// carries, pre-initialised so an instance where nobody has changed a Crew
// reads as a flat zero rather than as no-data.
var crewChangeOutcomes = []string{"ok", "rejected", "error"}

// crewOutcome classifies a Crew change error for the
// worklode_crew_changes_total outcome label. A refused change — an unknown
// actor, a role already held, a second lead, a member who still owns open
// work, the lead — is "rejected": it is the caller's input or the project's
// state, not a fault.
func crewOutcome(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalidInput) {
		return "rejected"
	}
	return "error"
}

// observeCrewChange records one attempted Crew membership change, called
// exactly once per attempt from both the JSON API handler (surface="api")
// and the roster page's form (surface="web").
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeCrewChange(surface, action, outcome string) {
	if s.crewChanges == nil {
		return
	}
	s.crewChanges.WithLabelValues(surface, action, outcome).Inc()
}

// milestoneChangeActions and milestoneChangeOutcomes are every label
// worklode_milestone_changes_total carries, pinned by the milestones plan and
// pre-initialised so an instance where nobody has touched a milestone reads
// as a flat zero rather than as no-data. task_attach and deliverable_attach
// belong to the attach mutations that land alongside this counter.
var (
	milestoneChangeActions  = []string{"create", "task_attach", "deliverable_attach"}
	milestoneChangeOutcomes = []string{"ok", "rejected", "error"}
)

// observeMilestoneChange records one attempted milestone change, called
// exactly once per attempt with the error the attempt returned. A refused
// change — a blank or over-long title, an unknown project, a child in another
// project — is "rejected": the caller's input, not a fault.
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeMilestoneChange(action string, err error) {
	if s.milestoneChanges == nil {
		return
	}
	outcome := "ok"
	switch {
	case err == nil:
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrInvalidInput):
		outcome = "rejected"
	default:
		outcome = "error"
	}
	s.milestoneChanges.WithLabelValues(action, outcome).Inc()
}

// repoMappingActions are every action label worklode_repo_mapping_changes_total
// carries: the three mutations `lode project repo` offers.
var repoMappingActions = []string{"add", "edit", "remove"}

// repoMappingOutcomes are every outcome label
// worklode_repo_mapping_changes_total carries, pre-initialised so an instance
// where nobody has touched a mapping reads as a flat zero rather than as
// no-data.
var repoMappingOutcomes = []string{"ok", "rejected", "error"}

// repoMappingOutcome classifies a mapping-change error for the outcome label.
// An unmapped repo, a repo already taken, an unusable done_state: all the
// caller's input, so "rejected", not a fault.
func repoMappingOutcome(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalidInput) ||
		errors.Is(err, store.ErrRepoTaken) {
		return "rejected"
	}
	return "error"
}

// observeRepoMapping records one attempted project/repo mapping change,
// called exactly once per attempt. Nil-safe: tests build a *server directly
// without initMetrics.
func (s *server) observeRepoMapping(action, outcome string) {
	if s.repoMappings == nil {
		return
	}
	s.repoMappings.WithLabelValues(action, outcome).Inc()
}

// recordLocalMerge records the result of one task named in a local merge
// report. Nil-safe: tests build a *server directly without initMetrics.
func (s *server) recordLocalMerge(result string) {
	if s.localMerges == nil {
		return
	}
	s.localMerges.WithLabelValues(result).Inc()
}

// cockpitOutcome classifies an assembleProjectCockpit error for the
// worklode_cockpit_projection_requests_total outcome label.
func cockpitOutcome(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, store.ErrNotFound) {
		return "not_found"
	}
	return "error"
}

// observeCockpitProjection records one attempted cockpit projection assembly,
// called exactly once per attempt from both the JSON API handler
// (surface="api") and the web project page (surface="web").
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeCockpitProjection(surface string, err error) {
	if s.cockpitProjections == nil {
		return
	}
	s.cockpitProjections.WithLabelValues(surface, cockpitOutcome(err)).Inc()
}

// observeNavigation records one web UI page request, by destination (see
// navWrap in web.go) and outcome ("ok", "not_found", "error", classified by
// navOutcome from the response status).
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeNavigation(destination, outcome string) {
	if s.navigations == nil {
		return
	}
	s.navigations.WithLabelValues(destination, outcome).Inc()
}

// The three Home render modes (see homePage), which are also this metric's
// only label values. ui.HomeView.Mode carries the same string into the
// template, so the label and the rendered markup cannot drift apart.
const (
	homeModeActor = "actor"
	homeModeOpen  = "open"
	homeModeEmpty = "empty"
)

var homeRenderModes = []string{homeModeActor, homeModeOpen, homeModeEmpty}

// observeHomeRender records one Home page render, by mode.
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeHomeRender(mode string) {
	if s.homeRenders == nil {
		return
	}
	s.homeRenders.WithLabelValues(mode).Inc()
}

// The run board's two render outcomes (see runboard.go's runBoardPage),
// which are also this metric's only label values: "empty" when
// assembleRunBoard found nothing to group, "rendered" otherwise.
const (
	runBoardRenderEmpty    = "empty"
	runBoardRenderRendered = "rendered"
)

var runBoardRenderOutcomes = []string{runBoardRenderEmpty, runBoardRenderRendered}

// observeRunBoardRender records one project run board page render, by
// outcome. Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeRunBoardRender(outcome string) {
	if s.runBoardRenders == nil {
		return
	}
	s.runBoardRenders.WithLabelValues(outcome).Inc()
}

// The inbox's two render outcomes (see inbox.go's inboxPage), which are also
// this metric's only label values: "empty" when assembleInbox found nothing
// waiting (including the signed-out case, which never calls it), "rendered"
// otherwise.
const (
	inboxRenderEmpty    = "empty"
	inboxRenderRendered = "rendered"
)

var inboxRenderOutcomes = []string{inboxRenderEmpty, inboxRenderRendered}

// observeInboxRender records one inbox page render, by outcome. Nil-safe:
// tests build a *server directly without initMetrics.
func (s *server) observeInboxRender(outcome string) {
	if s.inboxRenders == nil {
		return
	}
	s.inboxRenders.WithLabelValues(outcome).Inc()
}

// formOutcome classifies a creation-form error for the
// worklode_web_form_submissions_total outcome label. Rejected input and a
// refused cross-origin POST are recorded by their handlers as "invalid" and
// "forbidden" — neither is an error here.
func formOutcome(err error) string {
	if err == nil {
		return "created"
	}
	if errors.Is(err, store.ErrNotFound) {
		return "not_found"
	}
	if errors.Is(err, store.ErrInvalidInput) {
		return "invalid"
	}
	return "error"
}

// observeAuthz records one authorization decision, called exactly once per
// guarded request from requirePerm (API) and webGuard (web). permPublic never
// reaches an enforcement point, so it never appears as a label value.
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeAuthz(perm Permission, d Decision) {
	if s.authzDecisions == nil {
		return
	}
	outcome := "deny"
	if d.Allowed {
		outcome = "allow"
	}
	s.authzDecisions.WithLabelValues(string(perm), outcome).Inc()
}

// approvalDecisionKinds and approvalDecisionOutcomes are the bounded label
// values of worklode_approval_decisions_total. decisionInvalid stands for
// both a submission whose decision was not one of the three and one refused
// before a decision could be read (a cross-origin POST, an unreadable body).
var (
	approvalDecisionKinds    = []string{"approve", "request_changes", "reject", decisionInvalid}
	approvalDecisionOutcomes = []string{"resolved", "refused_self", "refused_role",
		"conflict", "not_found", "invalid", "error"}
)

// decisionInvalid is the decision label for a submission that named no valid
// decision, and the outcome label for refusing it.
const decisionInvalid = "invalid"

// observeApprovalDecision records one submitted approval decision, called
// exactly once per POST /approvals/{id}/decide that gets past requireSession.
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeApprovalDecision(decision, outcome string) {
	if s.approvalDecisions == nil {
		return
	}
	s.approvalDecisions.WithLabelValues(decision, outcome).Inc()
}

// approvalDecisionOutcome classifies a DecideApproval error for the outcome
// label. It mostly mirrors decideApprovalErr's status mapping, but not for
// ErrInvalidInput: that case is unreachable through decideApproval today (the
// decision string is validated before the store is called), and if it ever
// did fire, decideApprovalErr's default case would route it to webStoreErr,
// which reports 500 ("error"), not this function's "invalid".
func approvalDecisionOutcome(err error) string {
	switch {
	case err == nil:
		return "resolved"
	case errors.Is(err, store.ErrNotFound):
		return "not_found"
	case errors.Is(err, store.ErrApprovalResolved):
		return "conflict"
	case errors.Is(err, store.ErrSelfApproval):
		return "refused_self"
	case errors.Is(err, store.ErrNotQualified):
		return "refused_role"
	case errors.Is(err, store.ErrInvalidInput):
		return decisionInvalid
	default:
		return "error"
	}
}

// observeFormSubmission records one web creation-form submission, called
// exactly once per POST from webform.go.
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeFormSubmission(form, outcome string) {
	if s.formSubmissions == nil {
		return
	}
	s.formSubmissions.WithLabelValues(form, outcome).Inc()
}

// taskTokenOutcomes bounds worklode_task_tokens_total's one label.
var taskTokenOutcomes = []string{"ok", "not_found", "error"}

// observeTaskToken records one POST /tasks/{id}/tokens, by outcome.
// Nil-safe like every observer here.
func (s *server) observeTaskToken(outcome string) {
	if s.taskTokens == nil {
		return
	}
	s.taskTokens.WithLabelValues(outcome).Inc()
}

// dictationOutcomes bounds worklode_dictations_total's one label.
var dictationOutcomes = []string{"ok", "unconfigured", "too_large", "bad_request", "provider_error"}

// observeDictation records one POST /dictate, by outcome. Nil-safe like
// every observer here.
func (s *server) observeDictation(outcome string) {
	if s.dictations == nil {
		return
	}
	s.dictations.WithLabelValues(outcome).Inc()
}

// observeEventSubscriberSeek records one successful admin seek of a
// subscriber's offsets. Called on success only, matching observeAssignment.
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeEventSubscriberSeek(subscriber string) {
	if s.eventSubscriberSeeks == nil {
		return
	}
	s.eventSubscriberSeeks.WithLabelValues(subscriber).Inc()
}

// observeEventStreamOpen and observeEventStreamClose bracket one open
// follow; eventstream.go pairs them with a defer so a panicking handler still
// decrements.
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeEventStreamOpen() {
	if s.eventStreamsActive == nil {
		return
	}
	s.eventStreamsActive.Inc()
}

func (s *server) observeEventStreamClose() {
	if s.eventStreamsActive == nil {
		return
	}
	s.eventStreamsActive.Dec()
}

// observeEventStreamSent records n events pushed to one follower, called once
// per flushed batch rather than once per event.
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeEventStreamSent(n int) {
	if s.eventStreamEventsSent == nil {
		return
	}
	s.eventStreamEventsSent.Add(float64(n))
}

// observeListExpansion records one expanded list request. Nil-safe: tests
// build a *server directly without initMetrics.
func (s *server) observeListExpansion(endpoint, expansion string) {
	if s.listExpansions == nil {
		return
	}
	s.listExpansions.WithLabelValues(endpoint, expansion).Inc()
}

// blobUploadOutcomes and blobServeOutcomes are the complete, bounded label
// sets for the two blob families. They are enumerated here rather than being
// whatever the handlers happen to pass, so the label stays a closed set and
// initMetrics can pre-initialise every series.
var (
	blobUploadOutcomes = []string{
		"stored",        // new bytes written to the bucket and indexed
		"deduplicated",  // the hash was already known; no object write
		"too_large",     // over maxBlobBytes
		"empty",         // zero-length payload
		"unconfigured",  // no bucket on this instance
		"storage_error", // the object store refused the PUT
		"error",         // spool, rewind, index, or body-read failure
	}
	blobServeOutcomes = []string{
		"redirect",      // 302 to a presigned URL
		"not_found",     // no such hash in the index
		"unconfigured",  // no bucket on this instance
		"storage_error", // presign failed, or the index read did
	}
	// posterExtractionOutcomes is the complete, bounded label set for
	// worklode_video_poster_extractions_total.
	posterExtractionOutcomes = []string{
		"stored",        // a frame decoded and was indexed as its own blob
		"deduplicated",  // the same frame was already a known blob
		"unavailable",   // no ffmpeg in this image
		"failed",        // ffmpeg ran and could not produce a frame
		"storage_error", // the object store refused the PUT
		"error",         // indexing the poster failed
	}
	// taskBlobRefActions is the complete, bounded label set for
	// worklode_task_blob_refs_total.
	taskBlobRefActions = []string{"attached", "detached"}
	// blobGCModes and blobGCOutcomes are the complete, bounded label sets for
	// worklode_blob_gc_runs_total.
	blobGCModes    = []string{"dry_run", "apply"}
	blobGCOutcomes = []string{"ok", "error"}
	// blobGCObjectActions is the complete, bounded label set for
	// worklode_blob_gc_objects_total: what sweep 1 found (unreferenced) and
	// what sweep 2 found (orphan), plus how many of either were actually
	// deleted outside dry-run.
	blobGCObjectActions = []string{"unreferenced", "orphan", "deleted"}
)

// imageMirrorOutcomes is the complete, bounded label set for
// worklode_image_mirrors_total. The URL and its host are deliberately not
// labels: both are chosen by whoever filed the issue, so either would be an
// unbounded cardinality hole punched by an outsider. The URL is in the
// warning log, which is where a specific failure is diagnosed.
var imageMirrorOutcomes = []string{
	mirrorStored,        // fetched, sniffed embeddable, stored, rewritten
	mirrorDeduplicated,  // the hash was already indexed; no object write
	mirrorFetchFailed,   // the SSRF guard, the origin, or the budget refused
	mirrorNotEmbeddable, // fetched, but the bytes do not render in place
	mirrorStoreFailed,   // the bucket or the index refused
	mirrorRewriteFailed, // stored, but the body could not be rewritten
	mirrorCapped,        // over the per-body reference cap; skipped without a fetch
}

// mirrorTokenOutcomes is the complete label set for
// worklode_image_mirror_tokens_total. A pass on an instance with no GitHub App
// configured contributes nothing: not minting a token that was never
// configured is not an outcome of a mint.
var mirrorTokenOutcomes = []string{
	mirrorTokenMinted, // GitHub issued a token for the issue's repo
	mirrorTokenFailed, // the App is not installed there, or GitHub refused
}

const (
	mirrorTokenMinted = "minted"
	mirrorTokenFailed = "failed"
)

const (
	mirrorStored        = "mirrored"
	mirrorDeduplicated  = "deduplicated"
	mirrorFetchFailed   = "fetch_failed"
	mirrorNotEmbeddable = "not_embeddable"
	mirrorStoreFailed   = "store_failed"
	mirrorRewriteFailed = "rewrite_failed"
	mirrorCapped        = "capped"
)

// observeImageMirror adds n remote image references resolved to the named
// outcome. Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeImageMirror(outcome string, n int) {
	if s.imageMirrors == nil || n == 0 {
		return
	}
	s.imageMirrors.WithLabelValues(outcome).Add(float64(n))
}

// observeMirrorToken records one installation-token mint for a mirroring
// pass. Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeMirrorToken(outcome string) {
	if s.mirrorTokens == nil {
		return
	}
	s.mirrorTokens.WithLabelValues(outcome).Inc()
}

// observeBlobUpload records one POST /api/v1/blobs, called exactly once on
// every exit path of uploadBlob. Nil-safe: tests build a *server directly
// without initMetrics.
func (s *server) observeBlobUpload(outcome string) {
	if s.blobUploads == nil {
		return
	}
	s.blobUploads.WithLabelValues(outcome).Inc()
}

// observeBlobServe records one GET /blob/{hash}, called exactly once on every
// exit path of serveBlob. A refusal by eitherGuard is deliberately not
// counted here — the request never reached the handler, and
// worklode_authz_decisions_total is where a denial belongs.
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeBlobServe(outcome string) {
	if s.blobServes == nil {
		return
	}
	s.blobServes.WithLabelValues(outcome).Inc()
}

// observePosterExtraction records one attempt to extract a poster frame from
// an uploaded video, called exactly once on every exit path of storePoster.
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observePosterExtraction(outcome string) {
	if s.posterExtractions == nil {
		return
	}
	s.posterExtractions.WithLabelValues(outcome).Inc()
}

// observeTaskBlobRef records one attach or detach of a task's explicit blob
// reference. Called on the success path of attachTaskBlob and detachTaskBlob
// only. Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeTaskBlobRef(action string) {
	if s.taskBlobRefs == nil {
		return
	}
	s.taskBlobRefs.WithLabelValues(action).Inc()
}

// observeBlobGCRun records one GC sweep's mode and outcome. Called exactly
// once per invocation of blobGC. Nil-safe: tests build a *server directly
// without initMetrics.
func (s *server) observeBlobGCRun(dryRun bool, outcome string) {
	if s.blobGCRuns == nil {
		return
	}
	mode := "apply"
	if dryRun {
		mode = "dry_run"
	}
	s.blobGCRuns.WithLabelValues(mode, outcome).Inc()
}

// observeBlobGCObjects adds n to the named action's count. Nil-safe: tests
// build a *server directly without initMetrics.
func (s *server) observeBlobGCObjects(action string, n int) {
	if s.blobGCObjects == nil || n == 0 {
		return
	}
	s.blobGCObjects.WithLabelValues(action).Add(float64(n))
}

// The complete, bounded label sets for worklode_deletes_total (044 §6). The
// entity id is deliberately not a label: it is unbounded, and which rows were
// deleted is a question for the events log, not for a counter.
var (
	deleteEntities = []string{entityTask, entityDoc}
	deleteOps      = []string{opDelete, opUndelete}
	deleteOutcomes = []string{deleteOK, deleteJustificationRequired, deleteNotFound, deleteError}
)

const (
	entityTask = "task"
	entityDoc  = "doc"

	opDelete   = "delete"
	opUndelete = "undelete"

	deleteOK                    = "ok"
	deleteJustificationRequired = "justification_required"
	deleteNotFound              = "not_found"
	deleteError                 = "error"
)

// observeDelete records one delete or undelete attempt. It is called once on
// every path that reached a decision about the row — including the
// prod-instance refusal, which is an outcome of the delete op and not an
// absence of one. A request that never got that far, because its body or its
// id would not parse, is counted by the HTTP middleware as the 400 it is and
// not here: it named no delete to have an outcome.
//
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeDelete(entity, op, outcome string) {
	if s.deletes == nil {
		return
	}
	s.deletes.WithLabelValues(entity, op, outcome).Inc()
}

// The complete, bounded label sets for worklode_overview_reads_total (spec
// 007). The project filter is deliberately not a label: it is caller-chosen
// and unbounded, and which project someone looked at is a question for the
// access log, not for a counter.
var (
	overviewReadKinds    = []string{readOverview, readDrift, readGaps, readFrontier, readCriticalPath}
	overviewReadOutcomes = []string{overviewOK, overviewNoGraph, overviewErrored}
)

const (
	readOverview     = "overview"
	readDrift        = "drift"
	readGaps         = "gaps"
	readFrontier     = "frontier"
	readCriticalPath = "critical_path"

	overviewOK      = "ok"
	overviewNoGraph = "no_graph"
	overviewErrored = "error"
)

// overviewOutcome classifies an internal/overview error for the
// worklode_overview_reads_total outcome label. An unconfigured graph is its
// own outcome and not an error: the deployment has no endpoint, which is a
// standing fact about the instance rather than a failure of the request.
func overviewOutcome(err error) string {
	switch {
	case err == nil:
		return overviewOK
	case errors.Is(err, overview.ErrNoGraph):
		return overviewNoGraph
	default:
		return overviewErrored
	}
}

// observeOverviewRead records one spec 007 read, called exactly once on every
// exit path of each of the five handlers.
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeOverviewRead(read, outcome string) {
	if s.overviewReads == nil {
		return
	}
	s.overviewReads.WithLabelValues(read, outcome).Inc()
}

// The complete, bounded label sets for worklode_derive_runs_total. The graph
// IRI is not a label: it is one per source, so it would say nothing the
// source does not, and it would stop being bounded the day a deriver derives
// a graph per repo.
var (
	deriveSources  = []string{deriveDeploy, derivePRAffects}
	deriveOutcomes = []string{deriveWritten, deriveSkipped, deriveRefusedEmpty, deriveErrored}
)

const (
	deriveDeploy    = "deploy"
	derivePRAffects = "pr-affects"

	deriveWritten      = "written"       // the graph was replaced
	deriveSkipped      = "skipped"       // the content hash matched; no write
	deriveRefusedEmpty = "refused_empty" // derive.ErrWouldEmptyGraph
	deriveErrored      = "error"         // input read or PUT failed
)

// deriveOutcome classifies one derive.Run for the worklode_derive_runs_total
// outcome label. refused_empty is separated from error because it is the
// guard doing its job — a deriver produced nothing and was stopped from
// replacing a populated graph — and its rate is the one worth alerting on.
func deriveOutcome(res model.DeriveResult, err error) string {
	switch {
	case errors.Is(err, derive.ErrWouldEmptyGraph):
		return deriveRefusedEmpty
	case err != nil:
		return deriveErrored
	case res.Skipped:
		return deriveSkipped
	default:
		return deriveWritten
	}
}

// observeDeriveRun records one deriver's run, called once per deriver rather
// than once per POST /api/v1/derive: a request runs both, and a skipped
// no-op beside a replaced graph is exactly the distinction the request's
// status code cannot carry.
// Nil-safe: tests build a *server directly without initMetrics.
func (s *server) observeDeriveRun(source, outcome string) {
	if s.deriveRuns == nil {
		return
	}
	s.deriveRuns.WithLabelValues(source, outcome).Inc()
}

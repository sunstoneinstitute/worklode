// Package watch implements the wt watcher: a Kubernetes pod informer that
// detects crash-looping and OOM-killed containers and reports each one once
// to the work-tracker runtime-events API.
package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// crashLoopRestartThreshold is the restart count at or above which a
// CrashLoopBackOff container is reported. Below it the container may still
// be settling (image pull, config propagation).
const crashLoopRestartThreshold = 3

// resyncPeriod is the shared-informer resync interval. Each resync
// redelivers every pod, which is also how failed reports get retried.
const resyncPeriod = 5 * time.Minute

// Report is one detected runtime problem, ready to post to the server.
type Report struct {
	Cluster    string
	Kind       string // "crashloop" or "oom"
	Workload   string // "<namespace>/<workload name>"
	Image      string
	Message    string
	DedupeKey  string
	OccurredAt time.Time
}

// Reporter delivers one report; the server deduplicates on DedupeKey.
type Reporter interface {
	Report(ctx context.Context, r Report) error
}

// HTTPReporter posts reports to <server>/api/v1/runtime-events with a
// bearer token.
type HTTPReporter struct {
	serverURL string
	token     string
	client    *http.Client
}

// NewHTTPReporter returns an HTTPReporter for the given server base URL
// (scheme://host[:port], no trailing path) and bearer token.
func NewHTTPReporter(serverURL, token string) *HTTPReporter {
	return &HTTPReporter{
		serverURL: strings.TrimRight(serverURL, "/"),
		token:     token,
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Report posts r to the runtime-events endpoint. Both 201 (created) and 200
// (duplicate) count as success.
func (h *HTTPReporter) Report(ctx context.Context, r Report) error {
	body, err := json.Marshal(map[string]string{
		"cluster":     r.Cluster,
		"kind":        r.Kind,
		"workload":    r.Workload,
		"image":       r.Image,
		"message":     r.Message,
		"occurred_at": r.OccurredAt.UTC().Format(time.RFC3339),
		"dedupe_key":  r.DedupeKey,
	})
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.serverURL+"/api/v1/runtime-events", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("post runtime event: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("post runtime event: server returned %d: %s",
			resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return nil
}

// Watcher watches pods in all namespaces and reports crash loops and OOM
// kills. Create with New, then call Run.
//
// Reports are posted synchronously from the informer callbacks, so one slow
// or unreachable server call (up to the HTTP timeout, 10s) delays the
// following deliveries. Accepted v1 trade-off: the informer's queue absorbs
// the backlog during incident storms, and the server's idempotency makes
// restarts and redeliveries safe.
type Watcher struct {
	client  kubernetes.Interface
	cluster string
	rep     Reporter
	log     *slog.Logger

	mu   sync.Mutex
	seen map[string]struct{} // dedupe keys already reported successfully
}

// New builds a Watcher for one cluster. cluster is the name reported with
// every event; log may be nil for slog.Default().
func New(client kubernetes.Interface, cluster string, rep Reporter, log *slog.Logger) *Watcher {
	if log == nil {
		log = slog.Default()
	}
	return &Watcher{
		client:  client,
		cluster: cluster,
		rep:     rep,
		log:     log,
		seen:    map[string]struct{}{},
	}
}

// Run starts a shared informer on pods across all namespaces and blocks
// until ctx is cancelled. Detection runs on every add/update delivery
// (including resyncs); the in-memory seen-set keeps resyncs from re-posting
// and the server's idempotency is the backstop.
func (w *Watcher) Run(ctx context.Context) error {
	factory := informers.NewSharedInformerFactory(w.client, resyncPeriod)
	informer := factory.Core().V1().Pods().Informer()
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { w.handlePod(ctx, obj) },
		UpdateFunc: func(_, obj any) { w.handlePod(ctx, obj) },
		DeleteFunc: w.handlePodDelete,
	})
	if err != nil {
		return fmt.Errorf("add pod event handler: %w", err)
	}

	factory.Start(ctx.Done())
	defer factory.Shutdown()
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		// WaitForCacheSync only fails when ctx is cancelled: clean shutdown.
		return nil
	}
	w.log.Info("watching pods", "cluster", w.cluster)
	<-ctx.Done()
	return nil
}

// handlePod runs detection on one delivered pod and reports each finding
// not already in the seen-set. A failed report is logged and left out of
// the seen-set so the next delivery (update or resync) retries it.
func (w *Watcher) handlePod(ctx context.Context, obj any) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	for _, r := range w.detect(pod) {
		w.mu.Lock()
		_, done := w.seen[r.DedupeKey]
		w.mu.Unlock()
		if done {
			continue
		}
		if err := w.rep.Report(ctx, r); err != nil {
			w.log.Error("report runtime event",
				"dedupe_key", r.DedupeKey, "workload", r.Workload, "err", err)
			continue
		}
		w.mu.Lock()
		w.seen[r.DedupeKey] = struct{}{}
		w.mu.Unlock()
		w.log.Info("reported runtime event",
			"kind", r.Kind, "workload", r.Workload, "dedupe_key", r.DedupeKey)
	}
}

// handlePodDelete prunes the deleted pod's dedupe keys from the seen-set so
// a long-running watcher does not grow it without bound (keys embed the pod
// UID, so a deleted pod's entries can never match again). Prefix iteration
// over the whole map is fine at this scale.
func (w *Watcher) handlePodDelete(obj any) {
	if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = d.Obj
	}
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	prefix := fmt.Sprintf("%s/%s/", w.cluster, pod.UID)
	w.mu.Lock()
	for k := range w.seen {
		if strings.HasPrefix(k, prefix) {
			delete(w.seen, k)
		}
	}
	w.mu.Unlock()
}

// detect returns the reports warranted by pod's current container statuses.
// A container can yield both a crashloop and an oom report (an OOM kill is
// a common crash-loop cause); each dedupes independently.
func (w *Watcher) detect(pod *corev1.Pod) []Report {
	workload := workloadName(pod)
	var out []Report
	for _, cs := range pod.Status.ContainerStatuses {
		if wt := cs.State.Waiting; wt != nil && wt.Reason == "CrashLoopBackOff" &&
			cs.RestartCount >= crashLoopRestartThreshold {
			out = append(out, Report{
				Cluster:  w.cluster,
				Kind:     "crashloop",
				Workload: workload,
				Image:    cs.Image,
				Message: fmt.Sprintf("container %s in CrashLoopBackOff (restarts: %d)",
					cs.Name, cs.RestartCount),
				DedupeKey:  w.dedupeKey(pod, cs, "crashloop"),
				OccurredAt: time.Now().UTC(), // waiting state carries no timestamp
			})
		}
		if term := oomTermination(cs); term != nil {
			occurredAt := term.FinishedAt.Time
			if occurredAt.IsZero() {
				occurredAt = time.Now()
			}
			out = append(out, Report{
				Cluster:    w.cluster,
				Kind:       "oom",
				Workload:   workload,
				Image:      cs.Image,
				Message:    fmt.Sprintf("container %s OOMKilled", cs.Name),
				DedupeKey:  w.dedupeKey(pod, cs, "oom"),
				OccurredAt: occurredAt.UTC(),
			})
		}
	}
	return out
}

// oomTermination returns the OOMKilled termination record for cs, if any:
// lastState.terminated (the container has since restarted or is in backoff)
// or state.terminated (still sitting terminated).
func oomTermination(cs corev1.ContainerStatus) *corev1.ContainerStateTerminated {
	if t := cs.LastTerminationState.Terminated; t != nil && t.Reason == "OOMKilled" {
		return t
	}
	if t := cs.State.Terminated; t != nil && t.Reason == "OOMKilled" {
		return t
	}
	return nil
}

// dedupeKey identifies one escalation of one container's problem: the
// restart count makes each further restart a fresh report while repeated
// deliveries at the same count stay deduplicated.
func (w *Watcher) dedupeKey(pod *corev1.Pod, cs corev1.ContainerStatus, kind string) string {
	return fmt.Sprintf("%s/%s/%s/%s/%d", w.cluster, pod.UID, cs.Name, kind, cs.RestartCount)
}

// workloadName derives "<namespace>/<workload>" from a pod by walking its
// ownerReferences one level: a ReplicaSet owner has its trailing pod-template
// hash segment stripped (yielding the Deployment name); any other owner kind
// (StatefulSet, DaemonSet, Job) is used as-is; an ownerless pod uses its own
// name. The controller owner wins if one is marked.
func workloadName(pod *corev1.Pod) string {
	name := pod.Name
	if len(pod.OwnerReferences) > 0 {
		ref := pod.OwnerReferences[0]
		for _, r := range pod.OwnerReferences {
			if r.Controller != nil && *r.Controller {
				ref = r
				break
			}
		}
		name = ref.Name
		if ref.Kind == "ReplicaSet" {
			if i := strings.LastIndexByte(name, '-'); i > 0 {
				name = name[:i]
			}
		}
	}
	return pod.Namespace + "/" + name
}

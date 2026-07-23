package watch_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/sunstoneinstitute/worklode/internal/watch"
)

// fakeReporter records every Report call and can be told to fail the first
// N calls.
type fakeReporter struct {
	mu       sync.Mutex
	got      []watch.Report
	attempts int
	failN    int // fail this many calls before succeeding
}

func (f *fakeReporter) Report(_ context.Context, r watch.Report) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.attempts <= f.failN {
		return errors.New("boom")
	}
	f.got = append(f.got, r)
	return nil
}

func (f *fakeReporter) reports() []watch.Report {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]watch.Report, len(f.got))
	copy(out, f.got)
	return out
}

func (f *fakeReporter) attemptCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

// startWatcher runs a Watcher over client in the background and makes sure
// Run returns cleanly when the test ends.
func startWatcher(t *testing.T, client kubernetes.Interface, rep watch.Reporter) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- watch.New(client, "dev", rep, nil).Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run returned error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Run did not return after cancel")
		}
	})
}

// eventually polls cond until it returns true or the timeout expires.
func eventually(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v: %s", timeout, msg)
}

// crashLoopPod builds a pod whose single container "app" is waiting in
// CrashLoopBackOff with the given restart count.
func crashLoopPod(ns, name string, owner *metav1.OwnerReference, restarts int32) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      name,
			UID:       types.UID("uid-" + name),
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:         "app",
				Image:        "registry.example.com/sunstone/app:v1.2.3",
				RestartCount: restarts,
				State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
				},
			}},
		},
	}
	if owner != nil {
		pod.OwnerReferences = []metav1.OwnerReference{*owner}
	}
	return pod
}

// bumpPod updates the pod with a changed annotation so the informer
// delivers a fresh update event.
func bumpPod(t *testing.T, client kubernetes.Interface, pod *corev1.Pod, rev int) *corev1.Pod {
	t.Helper()
	pod.Annotations = map[string]string{"rev": strconv.Itoa(rev)}
	updated, err := client.CoreV1().Pods(pod.Namespace).Update(
		context.Background(), pod, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("update pod: %v", err)
	}
	return updated
}

func replicaSetOwner(name string) *metav1.OwnerReference {
	ctrl := true
	return &metav1.OwnerReference{Kind: "ReplicaSet", Name: name, Controller: &ctrl}
}

func TestCrashLoopReportAndDedupe(t *testing.T) {
	client := fake.NewClientset()
	rep := &fakeReporter{}
	startWatcher(t, client, rep)

	pod := crashLoopPod("ns1", "app-6d4b9c7f9-x2m4p", replicaSetOwner("app-6d4b9c7f9"), 5)
	pod, err := client.CoreV1().Pods("ns1").Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pod: %v", err)
	}

	eventually(t, 5*time.Second, func() bool { return len(rep.reports()) >= 1 },
		"first crashloop report")
	got := rep.reports()[0]
	if got.Kind != "crashloop" {
		t.Errorf("kind = %q, want crashloop", got.Kind)
	}
	if got.Cluster != "dev" {
		t.Errorf("cluster = %q, want dev", got.Cluster)
	}
	if got.Workload != "ns1/app" {
		t.Errorf("workload = %q, want ns1/app (ReplicaSet hash stripped)", got.Workload)
	}
	if got.Image != "registry.example.com/sunstone/app:v1.2.3" {
		t.Errorf("image = %q, want the container image", got.Image)
	}
	wantKey := fmt.Sprintf("dev/%s/app/crashloop/5", pod.UID)
	if got.DedupeKey != wantKey {
		t.Errorf("dedupe key = %q, want %q", got.DedupeKey, wantKey)
	}
	if got.Message != "container app in CrashLoopBackOff (restarts: 5)" {
		t.Errorf("message = %q", got.Message)
	}

	// Redelivery at the same restart count must not produce a second
	// report; an escalation to 6 restarts must. Informer deliveries are
	// ordered, so once the restarts-6 report arrives, a spurious duplicate
	// for restarts 5 would already be visible.
	pod = bumpPod(t, client, pod, 1)
	pod.Status.ContainerStatuses[0].RestartCount = 6
	bumpPod(t, client, pod, 2)

	eventually(t, 5*time.Second, func() bool { return len(rep.reports()) >= 2 },
		"escalation report at restarts 6")
	got2 := rep.reports()
	if len(got2) != 2 {
		t.Fatalf("reports = %d, want exactly 2 (dedupe failed): %+v", len(got2), got2)
	}
	wantKey6 := fmt.Sprintf("dev/%s/app/crashloop/6", pod.UID)
	if got2[1].DedupeKey != wantKey6 {
		t.Errorf("second dedupe key = %q, want %q", got2[1].DedupeKey, wantKey6)
	}
}

func TestOOMKilledReport(t *testing.T) {
	client := fake.NewClientset()
	rep := &fakeReporter{}
	startWatcher(t, client, rep)

	finished := metav1.NewTime(time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC))
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns1",
			Name:      "worker-0",
			UID:       types.UID("uid-worker-0"),
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:         "worker",
				Image:        "registry.example.com/sunstone/worker:v2",
				RestartCount: 1,
				State: corev1.ContainerState{
					Running: &corev1.ContainerStateRunning{},
				},
				LastTerminationState: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						Reason:     "OOMKilled",
						FinishedAt: finished,
					},
				},
			}},
		},
	}
	if _, err := client.CoreV1().Pods("ns1").Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create pod: %v", err)
	}

	eventually(t, 5*time.Second, func() bool { return len(rep.reports()) >= 1 }, "oom report")
	got := rep.reports()[0]
	if got.Kind != "oom" {
		t.Errorf("kind = %q, want oom", got.Kind)
	}
	if got.Workload != "ns1/worker-0" {
		t.Errorf("workload = %q, want ns1/worker-0 (bare pod)", got.Workload)
	}
	if got.Message != "container worker OOMKilled" {
		t.Errorf("message = %q", got.Message)
	}
	if !got.OccurredAt.Equal(finished.Time) {
		t.Errorf("occurred_at = %v, want terminated.finishedAt %v", got.OccurredAt, finished.Time)
	}
	wantKey := "dev/uid-worker-0/worker/oom/1"
	if got.DedupeKey != wantKey {
		t.Errorf("dedupe key = %q, want %q", got.DedupeKey, wantKey)
	}
}

func TestFailedReportRetries(t *testing.T) {
	client := fake.NewClientset()
	rep := &fakeReporter{failN: 1}
	startWatcher(t, client, rep)

	pod := crashLoopPod("ns1", "app-6d4b9c7f9-x2m4p", replicaSetOwner("app-6d4b9c7f9"), 5)
	pod, err := client.CoreV1().Pods("ns1").Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pod: %v", err)
	}

	// First delivery fails; the seen-set must not swallow the key.
	eventually(t, 5*time.Second, func() bool { return rep.attemptCount() >= 1 }, "first attempt")
	if len(rep.reports()) != 0 {
		t.Fatalf("reports after failed attempt = %d, want 0", len(rep.reports()))
	}

	// Next delivery of the same pod retries the same report.
	bumpPod(t, client, pod, 1)
	eventually(t, 5*time.Second, func() bool { return len(rep.reports()) == 1 }, "retried report")
	got := rep.reports()[0]
	wantKey := fmt.Sprintf("dev/%s/app/crashloop/5", pod.UID)
	if got.DedupeKey != wantKey {
		t.Errorf("retried dedupe key = %q, want %q", got.DedupeKey, wantKey)
	}
}

func TestSeenSetPrunedOnPodDelete(t *testing.T) {
	client := fake.NewClientset()
	rep := &fakeReporter{}
	startWatcher(t, client, rep)

	pod := crashLoopPod("ns1", "app-6d4b9c7f9-x2m4p", replicaSetOwner("app-6d4b9c7f9"), 5)
	if _, err := client.CoreV1().Pods("ns1").Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create pod: %v", err)
	}
	eventually(t, 5*time.Second, func() bool { return len(rep.reports()) >= 1 }, "first report")

	if err := client.CoreV1().Pods("ns1").Delete(context.Background(), pod.Name, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete pod: %v", err)
	}
	// Re-create the pod with the same UID and state: the delete must have
	// pruned its seen entries, so the identical dedupe key reports again.
	pod = crashLoopPod("ns1", "app-6d4b9c7f9-x2m4p", replicaSetOwner("app-6d4b9c7f9"), 5)
	if _, err := client.CoreV1().Pods("ns1").Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("re-create pod: %v", err)
	}

	eventually(t, 5*time.Second, func() bool { return len(rep.reports()) >= 2 },
		"report after prune")
	got := rep.reports()
	if len(got) != 2 {
		t.Fatalf("reports = %d, want 2: %+v", len(got), got)
	}
	if got[0].DedupeKey != got[1].DedupeKey {
		t.Errorf("dedupe keys differ: %q vs %q, want identical (proves pruning, not escalation)",
			got[0].DedupeKey, got[1].DedupeKey)
	}
}

func TestWorkloadFromStatefulSetOwner(t *testing.T) {
	client := fake.NewClientset()
	rep := &fakeReporter{}
	startWatcher(t, client, rep)

	ctrl := true
	owner := &metav1.OwnerReference{Kind: "StatefulSet", Name: "db", Controller: &ctrl}
	pod := crashLoopPod("ns1", "db-0", owner, 4)
	if _, err := client.CoreV1().Pods("ns1").Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create pod: %v", err)
	}

	eventually(t, 5*time.Second, func() bool { return len(rep.reports()) >= 1 }, "report")
	if got := rep.reports()[0].Workload; got != "ns1/db" {
		t.Errorf("workload = %q, want ns1/db (StatefulSet name as-is)", got)
	}
}

func TestBelowRestartThresholdNotReported(t *testing.T) {
	client := fake.NewClientset()
	rep := &fakeReporter{}
	startWatcher(t, client, rep)

	low := crashLoopPod("ns1", "app-6d4b9c7f9-low", replicaSetOwner("app-6d4b9c7f9"), 2)
	if _, err := client.CoreV1().Pods("ns1").Create(context.Background(), low, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create pod: %v", err)
	}
	// A second pod at the threshold acts as the ordering fence: when its
	// report arrives, the below-threshold pod's delivery has been handled.
	at := crashLoopPod("ns1", "app-6d4b9c7f9-at", replicaSetOwner("app-6d4b9c7f9"), 3)
	if _, err := client.CoreV1().Pods("ns1").Create(context.Background(), at, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create pod: %v", err)
	}

	eventually(t, 5*time.Second, func() bool { return len(rep.reports()) >= 1 }, "threshold report")
	got := rep.reports()
	if len(got) != 1 {
		t.Fatalf("reports = %d, want 1 (restarts < 3 must not report): %+v", len(got), got)
	}
	if got[0].DedupeKey != "dev/uid-app-6d4b9c7f9-at/app/crashloop/3" {
		t.Errorf("reported key = %q, want the restarts-3 pod", got[0].DedupeKey)
	}
}

func TestHTTPReporter(t *testing.T) {
	report := watch.Report{
		Cluster:    "dev",
		Kind:       "crashloop",
		Workload:   "ns1/app",
		Image:      "registry.example.com/sunstone/app:v1.2.3",
		Message:    "container app in CrashLoopBackOff (restarts: 5)",
		DedupeKey:  "dev/uid-1/app/crashloop/5",
		OccurredAt: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC),
	}

	t.Run("posts JSON with bearer token", func(t *testing.T) {
		var gotAuth string
		var gotBody map[string]string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			if r.URL.Path != "/api/v1/runtime-events" {
				t.Errorf("path = %q, want /api/v1/runtime-events", r.URL.Path)
			}
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Errorf("decode body: %v", err)
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":1,"status":"ok"}`)
		}))
		defer srv.Close()

		if err := watch.NewHTTPReporter(srv.URL, "tok123").Report(context.Background(), report); err != nil {
			t.Fatalf("Report: %v", err)
		}
		if gotAuth != "Bearer tok123" {
			t.Errorf("Authorization = %q, want Bearer tok123", gotAuth)
		}
		want := map[string]string{
			"cluster":     "dev",
			"kind":        "crashloop",
			"workload":    "ns1/app",
			"image":       "registry.example.com/sunstone/app:v1.2.3",
			"message":     "container app in CrashLoopBackOff (restarts: 5)",
			"occurred_at": "2026-07-19T12:00:00Z",
			"dedupe_key":  "dev/uid-1/app/crashloop/5",
		}
		for k, v := range want {
			if gotBody[k] != v {
				t.Errorf("body[%q] = %q, want %q", k, gotBody[k], v)
			}
		}
	})

	t.Run("duplicate 200 is success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"status":"duplicate"}`)
		}))
		defer srv.Close()
		if err := watch.NewHTTPReporter(srv.URL, "tok").Report(context.Background(), report); err != nil {
			t.Fatalf("Report on 200: %v", err)
		}
	})

	t.Run("non-2xx is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "nope", http.StatusUnprocessableEntity)
		}))
		defer srv.Close()
		if err := watch.NewHTTPReporter(srv.URL, "tok").Report(context.Background(), report); err == nil {
			t.Fatal("Report on 422: got nil error, want error")
		}
	})
}

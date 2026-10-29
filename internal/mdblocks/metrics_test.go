package mdblocks

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetricsNormalize(t *testing.T) {
	var nilM *Metrics
	if got := string(nilM.Normalize("doc", []byte("a\nb\n"))); got != "a b\n" {
		t.Errorf("nil receiver: %q", got)
	}

	m := NewMetrics(prometheus.NewRegistry())
	m.Normalize("rule", []byte("a\nb\n"))
	m.Normalize("rule", []byte("a b\n"))
	m.Normalize("task", []byte("a b\n"))
	for _, c := range []struct {
		kind, changed string
		want          float64
	}{{"rule", "true", 1}, {"rule", "false", 1}, {"task", "false", 1}, {"doc", "true", 0}} {
		if got := testutil.ToFloat64(m.normalize.WithLabelValues(c.kind, c.changed)); got != c.want {
			t.Errorf("%s changed=%s = %v, want %v", c.kind, c.changed, got, c.want)
		}
	}
}

package otlp

import (
	"fmt"
	"testing"
)

func TestLimiterAllowsTheBurstThenRefuses(t *testing.T) {
	t.Parallel()
	l := NewLimiter()
	for i := range limitBurst {
		if !l.Allow("actor-a") {
			t.Fatalf("request %d refused inside the burst of %d", i+1, limitBurst)
		}
	}
	if l.Allow("actor-a") {
		t.Fatal("request past the burst was allowed")
	}
}

func TestLimiterKeysAreIndependent(t *testing.T) {
	t.Parallel()
	l := NewLimiter()
	for range limitBurst + 1 {
		l.Allow("noisy")
	}
	if !l.Allow("quiet") {
		t.Fatal("one actor's burst starved another actor")
	}
}

func TestLimiterBucketMapStaysBounded(t *testing.T) {
	t.Parallel()
	l := NewLimiter()
	for i := range maxKeys * 2 {
		l.Allow(fmt.Sprintf("actor-%d", i))
	}
	if got := len(l.buckets); got > maxKeys {
		t.Fatalf("buckets = %d, want at most %d", got, maxKeys)
	}
}

func TestNilLimiterAllows(t *testing.T) {
	t.Parallel()
	var l *Limiter
	if !l.Allow("anyone") {
		t.Fatal("a nil limiter refused a request")
	}
}

package httpx

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestComplete_SucceedsFirstAttempt(t *testing.T) {
	var calls int
	got, err := Complete(context.Background(), "test", func(context.Context) Result {
		calls++
		return Result{Text: "answer"}
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "answer" {
		t.Errorf("text=%q want %q", got, "answer")
	}
	if calls != 1 {
		t.Errorf("calls=%d want 1 (a good first attempt must not retry)", calls)
	}
}

func TestComplete_RetriesHollowThenSucceeds(t *testing.T) {
	var calls int
	start := time.Now()
	got, err := Complete(context.Background(), "test", func(context.Context) Result {
		calls++
		if calls < 2 {
			return Result{Hollow: true}
		}
		return Result{Text: "recovered"}
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "recovered" {
		t.Errorf("text=%q want %q", got, "recovered")
	}
	if calls != 2 {
		t.Errorf("calls=%d want 2 (one hollow, then a good attempt)", calls)
	}
	// The second attempt waits one baseBackoff before firing.
	if elapsed := time.Since(start); elapsed < baseBackoff {
		t.Errorf("elapsed=%v want >= one backoff (%v)", elapsed, baseBackoff)
	}
}

func TestComplete_ExhaustsRetriesOnPersistentHollow(t *testing.T) {
	var calls int
	_, err := Complete(context.Background(), "openai", func(context.Context) Result {
		calls++
		return Result{Hollow: true}
	})
	if err == nil {
		t.Fatal("expected an error when every attempt is hollow")
	}
	if calls != maxAttempts {
		t.Errorf("calls=%d want %d (all attempts spent)", calls, maxAttempts)
	}
	// The error must name the provider and the failure mode.
	if !strings.Contains(err.Error(), "openai") || !strings.Contains(err.Error(), "empty completion") {
		t.Errorf("error %q should name the provider and the empty-completion failure", err)
	}
}

func TestComplete_TerminalErrorIsNotRetried(t *testing.T) {
	sentinel := errors.New("openai: API error (status 401): bad key")
	var calls int
	_, err := Complete(context.Background(), "openai", func(context.Context) Result {
		calls++
		return Result{Err: sentinel}
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("error=%v want the terminal error returned verbatim", err)
	}
	if calls != 1 {
		t.Errorf("calls=%d want 1 (a transport/API error must not be retried)", calls)
	}
}

func TestComplete_StopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	_, err := Complete(ctx, "test", func(context.Context) Result {
		calls++
		cancel() // cancel before the backoff wait of the next attempt
		return Result{Hollow: true}
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error=%v want context.Canceled", err)
	}
	if calls != 1 {
		t.Errorf("calls=%d want 1 (a cancelled context aborts the retry wait)", calls)
	}
}

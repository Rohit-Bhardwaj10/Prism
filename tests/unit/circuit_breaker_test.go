package unit

import (
	"errors"
	"testing"
	"time"

	"github.com/Rohit-Bhardwaj10/prism/internal/resilience"
)

func TestCircuitBreaker(t *testing.T) {
	cb := resilience.NewCircuitBreaker(3, 100*time.Millisecond)

	errDummy := errors.New("dummy error")

	// Phase 1: Normal failures
	for i := 0; i < 3; i++ {
		cb.Execute(func() error { return errDummy })
	}

	// Phase 2: Should be Open now
	err := cb.Execute(func() error { return nil })
	if !errors.Is(err, resilience.ErrCircuitOpen) {
		t.Fatalf("expected circuit open error, got %v", err)
	}

	// Phase 3: Wait for timeout
	time.Sleep(150 * time.Millisecond)

	// Phase 4: Should allow probe
	err = cb.Execute(func() error { return nil })
	if err != nil {
		t.Fatalf("expected success on half-open, got %v", err)
	}

	// Phase 5: Should be Closed again
	err = cb.Execute(func() error { return nil })
	if err != nil {
		t.Fatalf("expected success on closed, got %v", err)
	}
}

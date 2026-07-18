package unit

import (
	"testing"
	"time"

	"github.com/Rohit-Bhardwaj10/prism/internal/cache"
)

func TestL1Cache_LRUAndMemory(t *testing.T) {
	// 50 bytes max capacity
	l1 := cache.NewL1Cache(50)

	// Add 30 bytes (key "t1:q1" [5] + value [25]) = 30
	l1.Set("t1", "q1", "v1v1v1v1v1v1v1v1v1v1v1v1v", 0)

	// Add another 30 bytes -> total 60 -> should evict q1
	l1.Set("t2", "q2", "v2v2v2v2v2v2v2v2v2v2v2v2v", 0)

	_, ok := l1.Get("t1", "q1")
	if ok {
		t.Error("expected q1 to be evicted by memory budget")
	}

	_, ok = l1.Get("t2", "q2")
	if !ok {
		t.Error("expected q2 to exist")
	}
}

func TestL1Cache_TTL(t *testing.T) {
	l1 := cache.NewL1Cache(1000)

	// Set with 50ms TTL
	l1.Set("t1", "q1", "v1", 50*time.Millisecond)

	_, ok := l1.Get("t1", "q1")
	if !ok {
		t.Error("expected q1 to exist before expiry")
	}

	// Wait for expiry
	time.Sleep(100 * time.Millisecond)

	_, ok = l1.Get("t1", "q1")
	if ok {
		t.Error("expected q1 to be expired")
	}
}

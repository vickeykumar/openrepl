package gateway

import (
	"sync"
	"testing"
	"time"
)

func newTestRegistry(ttl time.Duration) (*SessionRegistry, *time.Time) {
	now := time.Unix(1000, 0)
	r := NewSessionRegistry(ttl)
	r.now = func() time.Time { return now }
	return r, &now
}

func TestRegistryCreateResolve(t *testing.T) {
	r, _ := newTestRegistry(time.Hour)
	id := Identity{Key: "g:abc", GuestID: "abc"}
	if _, ok := r.Resolve(id.Key); ok {
		t.Fatal("resolved a context that was never created")
	}
	ec := r.Create(id, "worker-1")
	if ec.BackendID != "worker-1" || ec.GuestID != "abc" {
		t.Fatalf("unexpected context: %+v", ec)
	}
	got, ok := r.Resolve(id.Key)
	if !ok || got.BackendID != "worker-1" {
		t.Fatalf("resolve = %+v, %v", got, ok)
	}
}

func TestRegistryCreateKeepsExistingBackend(t *testing.T) {
	r, _ := newTestRegistry(time.Hour)
	id := Identity{Key: "g:abc", GuestID: "abc"}
	r.Create(id, "worker-1")
	ec := r.Create(id, "worker-2")
	if ec.BackendID != "worker-1" {
		t.Fatalf("second Create re-chose the backend: %s", ec.BackendID)
	}
}

func TestRegistryGuestExpiryAndTouch(t *testing.T) {
	r, now := newTestRegistry(time.Hour)
	id := Identity{Key: "g:abc", GuestID: "abc"}
	r.Create(id, LocalID)

	*now = now.Add(59 * time.Minute)
	r.Touch(id.Key) // slides expiry to now+1h
	*now = now.Add(59 * time.Minute)
	if _, ok := r.Resolve(id.Key); !ok {
		t.Fatal("touched guest context expired early")
	}
	*now = now.Add(2 * time.Minute)
	if _, ok := r.Resolve(id.Key); ok {
		t.Fatal("idle guest context did not expire")
	}
	if r.Len() != 0 {
		t.Fatalf("expired context still stored: %d", r.Len())
	}
}

func TestRegistryUserContextDoesNotExpire(t *testing.T) {
	r, now := newTestRegistry(time.Hour)
	id := Identity{Key: "u:42", UID: "42"}
	r.Create(id, LocalID)
	*now = now.Add(1000 * time.Hour)
	if _, ok := r.Resolve(id.Key); !ok {
		t.Fatal("signed-in user's context expired")
	}
}

func TestRegistryRelease(t *testing.T) {
	r, _ := newTestRegistry(time.Hour)
	id := Identity{Key: "u:42", UID: "42"}
	r.Create(id, LocalID)
	r.Release(id.Key)
	if _, ok := r.Resolve(id.Key); ok {
		t.Fatal("released context still resolves")
	}
}

func TestRegistrySweepDropsExpired(t *testing.T) {
	r, now := newTestRegistry(time.Minute)
	r.Create(Identity{Key: "g:a", GuestID: "a"}, LocalID)
	*now = now.Add(2 * time.Minute)
	r.Create(Identity{Key: "g:b", GuestID: "b"}, LocalID) // sweeps g:a
	if r.Len() != 1 {
		t.Fatalf("len = %d, want 1", r.Len())
	}
}

func TestRegistryConcurrentCreateAgrees(t *testing.T) {
	r := NewSessionRegistry(time.Hour)
	id := Identity{Key: "g:abc", GuestID: "abc"}
	var wg sync.WaitGroup
	got := make([]string, 50)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i] = r.Create(id, "b"+string(rune('a'+i%26))).BackendID
		}(i)
	}
	wg.Wait()
	for _, b := range got {
		if b != got[0] {
			t.Fatalf("racing Creates disagreed: %v", got)
		}
	}
}

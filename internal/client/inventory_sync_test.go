// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSync answers the login and the sync route of one inventory, the sync
// with the replies queued in answers (the last one repeats), after delay;
// lastDelay, when set, replaces delay once the queue is down to its last
// reply.
type fakeSync struct {
	mu        sync.Mutex
	delay     time.Duration
	lastDelay time.Duration
	answers   []string
	syncs     int
}

func (f *fakeSync) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v1/auth/login/apikey":
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "t"})
	case "/api/v1/inventories/inv/sync":
		f.mu.Lock()
		f.syncs++
		answer, delay := f.answers[0], f.delay
		if len(f.answers) > 1 {
			f.answers = f.answers[1:]
		} else if f.lastDelay > 0 {
			delay = f.lastDelay
		}
		f.mu.Unlock()
		time.Sleep(delay)
		_, _ = w.Write([]byte(answer))
	case "/api/v1/inventories/inv":
		time.Sleep(f.delay)
		_, _ = w.Write([]byte(`{"id":"inv"}`))
	default:
		http.NotFound(w, r)
	}
}

// newSyncClient scales both deadlines down by the same factor as the code's
// 30 seconds and SyncTimeout, so the test keeps their ratio.
func newSyncClient(t *testing.T, f *fakeSync) *Client {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c := New(srv.URL, "key", "")
	scale := 300 // 5 minutes become 1s, 30 seconds 100ms
	c.http.Timeout /= time.Duration(scale)
	c.syncHTTP.Timeout /= time.Duration(scale)
	c.syncRetry = 10 * time.Millisecond
	return c
}

func TestSyncInventoryOutlastsTheGenericTimeout(t *testing.T) {
	f := &fakeSync{delay: 400 * time.Millisecond, answers: []string{`{"ok":true}`}}
	c := newSyncClient(t, f)
	if err := c.SyncInventory("inv"); err != nil {
		t.Fatalf("a sync slower than the generic timeout failed: %v", err)
	}
	// Every other call keeps the generic timeout.
	if _, err := c.GetInventory("inv"); err == nil {
		t.Fatal("an inventory read as slow as the sync did not time out")
	}
}

func TestSyncInventoryPastItsOwnTimeout(t *testing.T) {
	f := &fakeSync{delay: 1600 * time.Millisecond, answers: []string{`{"ok":true}`}}
	c := newSyncClient(t, f)
	err := c.SyncInventory("inv")
	if err == nil {
		t.Fatal("a sync slower than its own timeout succeeded")
	}
	for _, want := range []string{"within 1s", "may still be synchronizing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}
}

func TestSyncInventoryNotOKIsTheServersError(t *testing.T) {
	f := &fakeSync{answers: []string{`{"ok":false,"error":"Failed to synchronize inventory: key not found in secret"}`}}
	c := newSyncClient(t, f)
	err := c.SyncInventory("inv")
	if err == nil || err.Error() != "Failed to synchronize inventory: key not found in secret" {
		t.Fatalf("got %v, want the server's message", err)
	}
	if f.syncs != 1 {
		t.Errorf("a failed sync was asked %d times, want once", f.syncs)
	}
}

func TestSyncInventoryWaitsForARunningSync(t *testing.T) {
	busy := `{"ok":false,"error":"Failed to synchronize inventory: inventory refresh already in progress"}`
	f := &fakeSync{answers: []string{busy, busy, `{"ok":true}`}}
	c := newSyncClient(t, f)
	if err := c.SyncInventory("inv"); err != nil {
		t.Fatalf("a sync behind a running one failed: %v", err)
	}
	if f.syncs != 3 {
		t.Errorf("asked %d times, want 3", f.syncs)
	}
}

func TestSyncInventoryGivesUpOnARunningSyncAtItsTimeout(t *testing.T) {
	busy := `{"ok":false,"error":"Failed to synchronize inventory: inventory refresh already in progress"}`
	f := &fakeSync{answers: []string{busy}}
	c := newSyncClient(t, f)
	start := time.Now()
	err := c.SyncInventory("inv")
	// Whether the deadline falls during a pause or during a request, the
	// error says the server was busy.
	if err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("got %v, want the server's busy message", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("gave up after %s, well past the 1s timeout", took)
	}
}

// Waiting for a running sync spends the deadline: the attempt after the
// waits only gets what is left of it.
func TestSyncInventoryRetriesShareTheDeadline(t *testing.T) {
	busy := `{"ok":false,"error":"Failed to synchronize inventory: inventory refresh already in progress"}`
	f := &fakeSync{delay: 200 * time.Millisecond, lastDelay: 900 * time.Millisecond,
		answers: []string{busy, busy, busy, `{"ok":true}`}}
	c := newSyncClient(t, f)
	start := time.Now()
	err := c.SyncInventory("inv")
	for _, want := range []string{"within 1s", "already in progress"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("got %v, want it to say %q", err, want)
		}
	}
	if took := time.Since(start); took > 1500*time.Millisecond {
		t.Errorf("took %s, well past the 1s deadline", took)
	}
}

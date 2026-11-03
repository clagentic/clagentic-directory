package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/clagentic/clagentic-directory/internal/store"
)

type syncFakeStore struct {
	fakeStore
	status store.SyncStatus
}

func (s *syncFakeStore) SyncStatus() store.SyncStatus { return s.status }

func readyzFor(t *testing.T, st store.SyncStatus) (int, map[string]any) {
	t.Helper()
	s := &syncFakeStore{
		fakeStore: fakeStore{agents: map[string]store.Agent{"reviewer": newTestAgent()}},
		status:    st,
	}
	mux := http.NewServeMux()
	New(s, "").Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return rec.Code, body
}

func TestReadyzDegradesOnRepeatedFetchFailure(t *testing.T) {
	code, body := readyzFor(t, store.SyncStatus{ConsecutiveFailures: StaleFetchThreshold, LastSuccess: time.Now().Add(-time.Hour)})
	if code != http.StatusServiceUnavailable || body["status"] != "registry stale" {
		t.Fatalf("got %d %v, want 503 registry stale", code, body)
	}
	if _, ok := body["sync"].(map[string]any)["last_success"]; !ok {
		t.Fatalf("sync.last_success missing: %v", body)
	}
}

func TestReadyzOkBelowThreshold(t *testing.T) {
	code, body := readyzFor(t, store.SyncStatus{ConsecutiveFailures: StaleFetchThreshold - 1, LastSuccess: time.Now()})
	if code != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("got %d %v, want 200 ok", code, body)
	}
}

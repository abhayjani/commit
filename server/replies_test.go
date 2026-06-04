package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/msfoundry/commit/store"
)

// Exercises the real router + auth + handlers for the new endpoints, without
// booting the full binary (which would touch /etc/hosts and open a browser).
func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.SetPasscode("test-passcode"); err != nil {
		t.Fatalf("set passcode: %v", err)
	}
	s := &Server{db: db, port: 9384, startedAt: time.Now()}
	s.mux = http.NewServeMux()
	s.registerRoutes()
	token := s.generateSession() // registers a valid session
	return s, token
}

func get(t *testing.T, s *Server, token, path string) (int, map[string]json.RawMessage) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.AddCookie(&http.Cookie{Name: "commit_session", Value: token})
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	var body map[string]json.RawMessage
	json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func TestRepliesEndpoint(t *testing.T) {
	s, token := newTestServer(t)

	code, body := get(t, s, token, "/api/replies")
	if code != 200 {
		t.Fatalf("/api/replies status = %d, want 200", code)
	}
	if _, ok := body["needs_reply"]; !ok {
		t.Errorf("/api/replies missing needs_reply key: %v", body)
	}
	if _, ok := body["awaiting_reply"]; !ok {
		t.Errorf("/api/replies missing awaiting_reply key: %v", body)
	}

	code, stats := get(t, s, token, "/api/commitments/stats")
	if code != 200 {
		t.Fatalf("/api/commitments/stats status = %d, want 200", code)
	}
	for _, k := range []string{"needs_reply", "awaiting_reply", "open"} {
		if _, ok := stats[k]; !ok {
			t.Errorf("stats missing %q: %v", k, stats)
		}
	}

	code, exp := get(t, s, token, "/api/export")
	if code != 200 {
		t.Fatalf("/api/export status = %d, want 200", code)
	}
	for _, k := range []string{"exported_at", "commitments", "replies"} {
		if _, ok := exp[k]; !ok {
			t.Errorf("export missing %q: %v", k, exp)
		}
	}
}

func TestRepliesRequiresAuth(t *testing.T) {
	s, _ := newTestServer(t)
	req := httptest.NewRequest("GET", "/api/replies", nil) // no cookie
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("/api/replies without auth = %d, want 401", rec.Code)
	}
}

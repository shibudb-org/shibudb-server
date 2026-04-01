package server

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/shibudb.org/shibudb-server/internal/auth"
)

func TestManagementServerRequiresBearerToken(t *testing.T) {
	dataDir := t.TempDir()
	connMgr := NewConnectionManager(100, dataDir)
	defer connMgr.Shutdown()

	tokenMgr, err := auth.NewTokenManager(filepath.Join(dataDir, "management_tokens.json"))
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	ms := NewManagementServer(connMgr, tokenMgr, "0")

	tests := []struct {
		name         string
		method       string
		path         string
		authHeader   string
		wantHTTPCode int
	}{
		{
			name:         "health missing authorization header",
			method:       http.MethodGet,
			path:         "/health",
			authHeader:   "",
			wantHTTPCode: http.StatusForbidden,
		},
		{
			name:         "health invalid bearer token",
			method:       http.MethodGet,
			path:         "/health",
			authHeader:   "Bearer invalid-token",
			wantHTTPCode: http.StatusForbidden,
		},
		{
			name:         "stats missing authorization header",
			method:       http.MethodGet,
			path:         "/stats",
			authHeader:   "",
			wantHTTPCode: http.StatusForbidden,
		},
		{
			name:         "limit missing authorization header",
			method:       http.MethodGet,
			path:         "/limit",
			authHeader:   "",
			wantHTTPCode: http.StatusForbidden,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()

			ms.server.Handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantHTTPCode {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantHTTPCode)
			}
		})
	}
}

func TestManagementServerAcceptsValidBearerToken(t *testing.T) {
	dataDir := t.TempDir()
	connMgr := NewConnectionManager(100, dataDir)
	defer connMgr.Shutdown()

	tokenMgr, err := auth.NewTokenManager(filepath.Join(dataDir, "management_tokens.json"))
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	_, rawToken, err := tokenMgr.GenerateToken("admin")
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	ms := NewManagementServer(connMgr, tokenMgr, "0")

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	rec := httptest.NewRecorder()

	ms.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestManagementStatsExposeBusyAndIdleConnections(t *testing.T) {
	dataDir := t.TempDir()
	connMgr := NewConnectionManager(100, dataDir)
	defer connMgr.Shutdown()

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	if !connMgr.TryAcquire(serverConn) {
		t.Fatal("TryAcquire() = false, want true")
	}
	defer connMgr.Release(serverConn)
	connMgr.MarkConnectionBusy(serverConn)
	defer connMgr.MarkConnectionIdle(serverConn)

	tokenMgr, err := auth.NewTokenManager(filepath.Join(dataDir, "management_tokens.json"))
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	_, rawToken, err := tokenMgr.GenerateToken("admin")
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	ms := NewManagementServer(connMgr, tokenMgr, "0")

	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	rec := httptest.NewRecorder()

	ms.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var payload map[string]float64
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if got := int(payload["active_connections"]); got != 1 {
		t.Fatalf("active_connections = %d, want 1", got)
	}
	if got := int(payload["busy_connections"]); got != 1 {
		t.Fatalf("busy_connections = %d, want 1", got)
	}
	if got := int(payload["idle_connections"]); got != 0 {
		t.Fatalf("idle_connections = %d, want 0", got)
	}
	if got := int(payload["available_slots"]); got != 99 {
		t.Fatalf("available_slots = %d, want 99", got)
	}
}

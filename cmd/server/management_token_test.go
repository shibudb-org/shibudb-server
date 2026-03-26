package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagementAPIAuthHandler(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	h := managementAPIAuthHandler("secret-token", inner)

	t.Run("rejects without header", func(t *testing.T) {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("got %d", rr.Code)
		}
	})

	t.Run("accepts X-ShibuDB-Management-Token", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		req.Header.Set(ManagementAPITokenHeader, "secret-token")
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusTeapot {
			t.Fatalf("got %d", rr.Code)
		}
	})

	t.Run("accepts Authorization Bearer", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusTeapot {
			t.Fatalf("got %d", rr.Code)
		}
	})

	t.Run("rejects wrong token", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		req.Header.Set(ManagementAPITokenHeader, "wrong")
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("got %d", rr.Code)
		}
	})
}

func TestResolveManagementAPIToken_envOverridesFile(t *testing.T) {
	t.Setenv("SHIBUDB_MANAGEMENT_API_TOKEN", "from-env")
	dir := t.TempDir()
	path := filepath.Join(dir, ManagementAPITokenFile)
	if err := os.WriteFile(path, []byte("from-file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := ResolveManagementAPIToken(dir); got != "from-env" {
		t.Fatalf("got %q", got)
	}
}

func TestGenerateAndWriteManagementAPIToken(t *testing.T) {
	t.Setenv("SHIBUDB_MANAGEMENT_API_TOKEN", "")
	dir := t.TempDir()
	tok, err := GenerateAndWriteManagementAPIToken(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(tok)) < 32 {
		t.Fatalf("token too short: %q", tok)
	}
	if ResolveManagementAPIToken(dir) != tok {
		t.Fatalf("read back mismatch")
	}
	_, err = GenerateAndWriteManagementAPIToken(dir, false)
	if err == nil {
		t.Fatal("expected error without overwrite")
	}
}
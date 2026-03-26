package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	// ManagementAPITokenFile is stored under the server lib directory (alongside users.json).
	ManagementAPITokenFile = "management_api_token"
	// ManagementAPITokenHeader is the HTTP header clients must send when management auth is enabled.
	ManagementAPITokenHeader = "X-ShibuDB-Management-Token"
)

// ResolveManagementAPIToken returns the token the management HTTP API should require.
// Non-empty SHIBUDB_MANAGEMENT_API_TOKEN overrides the on-disk file.
// If both are empty or missing, the management API stays open (legacy behavior).
func ResolveManagementAPIToken(libDir string) string {
	if t := strings.TrimSpace(os.Getenv("SHIBUDB_MANAGEMENT_API_TOKEN")); t != "" {
		return t
	}
	data, err := os.ReadFile(filepath.Join(libDir, ManagementAPITokenFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// GenerateAndWriteManagementAPIToken creates a random token and writes it to libDir/management_api_token (mode 0600).
func GenerateAndWriteManagementAPIToken(libDir string, overwrite bool) (string, error) {
	path := filepath.Join(libDir, ManagementAPITokenFile)
	if _, err := os.Stat(path); err == nil && !overwrite {
		return "", fmt.Errorf("management token file already exists at %s (use --force to replace)", path)
	}
	if err := os.MkdirAll(libDir, 0755); err != nil {
		return "", fmt.Errorf("create lib directory: %w", err)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(token+"\n"), 0600); err != nil {
		return "", err
	}
	return token, nil
}

// managementAPIAuthHandler requires a matching token via X-ShibuDB-Management-Token or Authorization: Bearer <token>.
func managementAPIAuthHandler(expectedToken string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimSpace(r.Header.Get(ManagementAPITokenHeader))
		if got == "" {
			auth := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if strings.HasPrefix(auth, prefix) {
				got = strings.TrimSpace(auth[len(prefix):])
			}
		}
		if !constantTimeEqualToken(expectedToken, got) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "missing or invalid management API token",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func constantTimeEqualToken(a, b string) bool {
	// Avoid leaking length differences for variable-length secrets when lengths match.
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
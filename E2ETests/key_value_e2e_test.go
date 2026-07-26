package E2ETests

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/shibudb.org/shibudb-server/internal/models"
)

// kvResponse mirrors the server's JSON reply. Value is only set for GET.
type kvResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Value   string `json:"value"`
}

// sendAndParse sends a query and decodes the JSON response so tests can assert
// exact value equality (the older helpers only do substring matching, which
// cannot distinguish values containing quotes, unicode escapes, etc).
func sendAndParse(t *testing.T, q models.Query, conn net.Conn, reader *bufio.Reader) kvResponse {
	t.Helper()
	raw := sendQueryAndGetResponse(q, conn, reader)
	var resp kvResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &resp); err != nil {
		t.Fatalf("failed to parse server response %q: %v", raw, err)
	}
	return resp
}

func kvConnect(t *testing.T) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", "localhost:4444")
	if err != nil {
		t.Fatalf("TCP connection error: %v", err)
	}
	reader := bufio.NewReader(conn)
	if !Login("admin", "admin", conn, reader) {
		conn.Close()
		t.Fatal("Setup failed: Login failed")
	}
	return conn, reader
}

func kvCreateSpace(t *testing.T, space string, conn net.Conn, reader *bufio.Reader) {
	t.Helper()
	CleanSpace(space, conn, reader)
	if !CreateSpaceWithSettings(models.Query{
		Type:       models.TypeCreateSpace,
		Space:      space,
		EngineType: "key-value",
		EnableWAL:  true,
	}, conn, reader) {
		t.Fatalf("failed to create key-value space %s", space)
	}
}

func kvPut(t *testing.T, space, key, value string, conn net.Conn, reader *bufio.Reader) {
	t.Helper()
	resp := sendAndParse(t, models.Query{Type: models.TypePut, Space: space, Key: key, Value: value}, conn, reader)
	if resp.Status != "OK" {
		t.Fatalf("PUT %q failed: %+v", key, resp)
	}
}

func kvGet(t *testing.T, space, key string, conn net.Conn, reader *bufio.Reader) kvResponse {
	t.Helper()
	return sendAndParse(t, models.Query{Type: models.TypeGet, Space: space, Key: key}, conn, reader)
}

func kvExpectValue(t *testing.T, space, key, want string, conn net.Conn, reader *bufio.Reader) {
	t.Helper()
	resp := kvGet(t, space, key, conn, reader)
	if resp.Status != "OK" {
		t.Fatalf("GET %q failed: %+v", key, resp)
	}
	if resp.Value != want {
		t.Fatalf("GET %q = %q, want %q", key, resp.Value, want)
	}
}

// TestKeyValueOverwriteAndRePutE2E verifies last-writer-wins semantics over the
// wire: overwriting an existing key, and re-putting a key after deleting it.
func TestKeyValueOverwriteAndRePutE2E(t *testing.T) {
	conn, reader := kvConnect(t)
	defer conn.Close()

	space := "kv_overwrite_e2e"
	kvCreateSpace(t, space, conn, reader)
	defer CleanSpace(space, conn, reader)

	kvPut(t, space, "k", "v1", conn, reader)
	kvExpectValue(t, space, "k", "v1", conn, reader)

	kvPut(t, space, "k", "v2", conn, reader)
	kvExpectValue(t, space, "k", "v2", conn, reader)

	resp := sendAndParse(t, models.Query{Type: models.TypeDelete, Space: space, Key: "k"}, conn, reader)
	if resp.Status != "OK" || resp.Message != "DELETED" {
		t.Fatalf("DELETE failed: %+v", resp)
	}
	if got := kvGet(t, space, "k", conn, reader); got.Status != "ERROR" {
		t.Fatalf("GET after DELETE should error, got %+v", got)
	}

	kvPut(t, space, "k", "v3", conn, reader)
	kvExpectValue(t, space, "k", "v3", conn, reader)
}

// TestKeyValueSpecialValuesE2E verifies that values with characters that could
// break JSON transport or the on-disk record format round-trip byte-for-byte.
func TestKeyValueSpecialValuesE2E(t *testing.T) {
	conn, reader := kvConnect(t)
	defer conn.Close()

	space := "kv_special_values_e2e"
	kvCreateSpace(t, space, conn, reader)
	defer CleanSpace(space, conn, reader)

	cases := []struct {
		name  string
		key   string
		value string
	}{
		{"json_object", "jsonKey", `{"name":"test","nested":{"a":[1,2,3]},"quote":"he said \"hi\""}`},
		{"unicode", "unicodeKey", "こんにちは世界 – ключ значение – 🚀"},
		{"spaces_and_tabs", "spacedKey", "value with spaces\tand\ttabs"},
		{"newlines", "multilineKey", "line one\nline two\r\nline three"},
		{"quotes_backslashes", "escapeKey", `back\slash "double" 'single' and \n literal`},
		{"numeric_string", "numKey", "0003.14000"},
		{"single_char", "tinyKey", "x"},
		{"large_8kb", "largeKey", strings.Repeat("Lorem ipsum dolor sit amet. ", 300)},
		{"unicode_key", "клавиша-キー", "value for unicode key"},
		{"special_chars_key", "key:with/slashes.and.dots|pipe", "value for special key"},
	}

	for _, tc := range cases {
		kvPut(t, space, tc.key, tc.value, conn, reader)
	}
	// Read back after all writes so earlier writes may have been flushed to the
	// data file while later ones are still in the in-memory batch.
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kvExpectValue(t, space, tc.key, tc.value, conn, reader)
		})
	}
}

// TestKeyValueBulkE2E writes a larger set of keys, deletes a subset, and
// verifies every key resolves to exactly the right state afterwards.
func TestKeyValueBulkE2E(t *testing.T) {
	conn, reader := kvConnect(t)
	defer conn.Close()

	space := "kv_bulk_e2e"
	kvCreateSpace(t, space, conn, reader)
	defer CleanSpace(space, conn, reader)

	const total = 100
	for i := 0; i < total; i++ {
		kvPut(t, space, fmt.Sprintf("bulk-key-%03d", i), fmt.Sprintf("bulk-value-%03d", i), conn, reader)
	}

	// Delete every third key.
	for i := 0; i < total; i += 3 {
		key := fmt.Sprintf("bulk-key-%03d", i)
		resp := sendAndParse(t, models.Query{Type: models.TypeDelete, Space: space, Key: key}, conn, reader)
		if resp.Status != "OK" {
			t.Fatalf("DELETE %q failed: %+v", key, resp)
		}
	}

	for i := 0; i < total; i++ {
		key := fmt.Sprintf("bulk-key-%03d", i)
		resp := kvGet(t, space, key, conn, reader)
		if i%3 == 0 {
			if resp.Status != "ERROR" {
				t.Fatalf("GET deleted key %q should error, got %+v", key, resp)
			}
			continue
		}
		want := fmt.Sprintf("bulk-value-%03d", i)
		if resp.Status != "OK" || resp.Value != want {
			t.Fatalf("GET %q = %+v, want value %q", key, resp, want)
		}
	}
}

// TestKeyValueEmptyValueRejectedE2E verifies the server rejects empty values
// (they are unrepresentable in the storage format) and that a rejected PUT
// neither creates a key nor clobbers an existing value.
func TestKeyValueEmptyValueRejectedE2E(t *testing.T) {
	conn, reader := kvConnect(t)
	defer conn.Close()

	space := "kv_empty_value_e2e"
	kvCreateSpace(t, space, conn, reader)
	defer CleanSpace(space, conn, reader)

	resp := sendAndParse(t, models.Query{Type: models.TypePut, Space: space, Key: "k", Value: ""}, conn, reader)
	if resp.Status != "ERROR" || !strings.Contains(resp.Message, "empty value") {
		t.Fatalf("PUT with empty value should be rejected, got %+v", resp)
	}
	if got := kvGet(t, space, "k", conn, reader); got.Status != "ERROR" {
		t.Fatalf("GET after rejected PUT should error, got %+v", got)
	}

	kvPut(t, space, "k", "real-value", conn, reader)
	resp = sendAndParse(t, models.Query{Type: models.TypePut, Space: space, Key: "k", Value: ""}, conn, reader)
	if resp.Status != "ERROR" {
		t.Fatalf("second empty PUT should be rejected, got %+v", resp)
	}
	kvExpectValue(t, space, "k", "real-value", conn, reader)
}

// TestKeyValueGetNonExistentE2E verifies GET and DELETE of a missing key both
// surface a clear error rather than an empty success.
func TestKeyValueGetNonExistentE2E(t *testing.T) {
	conn, reader := kvConnect(t)
	defer conn.Close()

	space := "kv_missing_key_e2e"
	kvCreateSpace(t, space, conn, reader)
	defer CleanSpace(space, conn, reader)

	if resp := kvGet(t, space, "never-written", conn, reader); resp.Status != "ERROR" || !strings.Contains(resp.Message, "not found") {
		t.Fatalf("GET missing key should return not-found error, got %+v", resp)
	}
	resp := sendAndParse(t, models.Query{Type: models.TypeDelete, Space: space, Key: "never-written"}, conn, reader)
	if resp.Status != "ERROR" || !strings.Contains(resp.Message, "not found") {
		t.Fatalf("DELETE missing key should return not-found error, got %+v", resp)
	}
}

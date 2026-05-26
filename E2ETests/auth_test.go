package E2ETests

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/shibudb.org/shibudb-server/internal/models"
)

var globalConn net.Conn
var globalReader *bufio.Reader

func TestMain(m *testing.M) {
	// Setup
	var err error
	globalConn, err = net.Dial("tcp", "localhost:4444")
	if err != nil {
		fmt.Println("Setup failed: TCP connection error")
		os.Exit(1)
	}
	globalReader = bufio.NewReader(globalConn)

	if !Login("admin", "admin", globalConn, globalReader) {
		fmt.Println("Setup failed: Login failed")
		os.Exit(1)
	}

	CleanSpace("auth_test", globalConn, globalReader)
	CreateSpaceWithIndex("auth_test", "key-value", 0, "Flat", "L2", globalConn, globalReader)

	// Run tests
	exitCode := m.Run()

	// Teardown
	globalConn.Close()

	os.Exit(exitCode)
}

func TestAdminLogin(t *testing.T) {
	conn, err := net.Dial("tcp", "localhost:4444")
	if err != nil {
		t.Errorf("TCP error")
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	success := Login("admin", "admin", conn, reader)

	if !success {
		t.Errorf("Login failed")
	}
}

func TestSpaceReadAccess(t *testing.T) {
	CleanSpace("ts1", globalConn, globalReader)
	CleanUser("ts1", globalConn, globalReader)
	success := CreateSpaceWithIndex("ts1", "key-value", 0, "Flat", "L2", globalConn, globalReader)
	if !success {
		t.Errorf("Table space creation failed")
	}
	permissions := map[string]string{}
	permissions["ts1"] = "read"
	success = CreateUser("admin", "ts1", "ts1p", "user", permissions, globalConn, globalReader)
	if !success {
		t.Errorf("User creation failed")
	}

	conn, err := net.Dial("tcp", "localhost:4444")
	if err != nil {
		t.Errorf("TCP error")
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	success = Login("ts1", "ts1p", conn, reader)

	if !success {
		t.Errorf("TS1 Login failed")
	}

	query := models.Query{Type: models.TypeGet, Key: "key1", Space: "ts1", User: "ts1"}
	data, _ := json.Marshal(query)
	conn.Write(append(data, '\n'))

	resp, err := reader.ReadString('\n')
	if err != nil {
		t.Errorf("Server error")
		fmt.Println("Server response error:", err)
	}

	if strings.Contains(resp, `permission denied`) {
		t.Errorf("Unexpected permission denied")
	}

	query = models.Query{Type: models.TypePut, Key: "key1", Value: "val1", Space: "ts1", User: "ts1"}
	data, _ = json.Marshal(query)
	conn.Write(append(data, '\n'))

	resp, err = reader.ReadString('\n')
	if err != nil {
		t.Errorf("Server error")
		fmt.Println("Server response error:", err)
	}

	if !strings.Contains(resp, `permission denied`) {
		t.Errorf("Expected permission denied")
	}

	query = models.Query{Type: models.TypeDelete, Key: "key1", Space: "ts1", User: "ts1"}
	data, _ = json.Marshal(query)
	conn.Write(append(data, '\n'))

	resp, err = reader.ReadString('\n')
	if err != nil {
		t.Errorf("Server error")
		fmt.Println("Server response error:", err)
	}

	if !strings.Contains(resp, `permission denied`) {
		t.Errorf("Expected permission denied")
	}
}

func TestSpaceWriteAccess(t *testing.T) {
	CleanSpace("ts1", globalConn, globalReader)
	CleanUser("ts1", globalConn, globalReader)
	success := CreateSpaceWithIndex("ts1", "key-value", 0, "Flat", "L2", globalConn, globalReader)
	if !success {
		t.Errorf("Table space creation failed")
	}
	permissions := map[string]string{}
	permissions["ts1"] = "write"
	success = CreateUser("admin", "ts1", "ts1p", "user", permissions, globalConn, globalReader)
	if !success {
		t.Errorf("User creation failed")
	}

	conn, err := net.Dial("tcp", "localhost:4444")
	if err != nil {
		t.Errorf("TCP error")
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	success = Login("ts1", "ts1p", conn, reader)

	if !success {
		t.Errorf("TS1 Login failed")
	}

	query := models.Query{Type: models.TypeGet, Key: "key1", Space: "ts1", User: "ts1"}
	data, _ := json.Marshal(query)
	conn.Write(append(data, '\n'))

	resp, err := reader.ReadString('\n')
	if err != nil {
		t.Errorf("Server error")
		fmt.Println("Server response error:", err)
	}

	if strings.Contains(resp, `permission denied`) {
		t.Errorf("Unexpected permission denied")
	}

	query = models.Query{Type: models.TypePut, Key: "key1", Value: "val1", Space: "ts1", User: "ts1"}
	data, _ = json.Marshal(query)
	conn.Write(append(data, '\n'))

	resp, err = reader.ReadString('\n')
	if err != nil {
		t.Errorf("Server error")
		fmt.Println("Server response error:", err)
	}

	if strings.Contains(resp, `permission denied`) {
		t.Errorf("Unexpected permission denied")
	}

	query = models.Query{Type: models.TypeDelete, Key: "key1", Space: "ts1", User: "ts1"}
	data, _ = json.Marshal(query)
	conn.Write(append(data, '\n'))

	resp, err = reader.ReadString('\n')
	if err != nil {
		t.Errorf("Server error")
		fmt.Println("Server response error:", err)
	}

	if strings.Contains(resp, `permission denied`) {
		t.Errorf("Unexpected permission denied")
	}
}

// Vector Engine Tests

func TestVectorSpaceReadAccess(t *testing.T) {
	CleanSpace("vector_test", globalConn, globalReader)
	CleanUser("vector_test", globalConn, globalReader)
	success := CreateVectorSpace("vector_test", 128, "Flat", "L2", globalConn, globalReader)
	if !success {
		t.Errorf("Vector space creation failed")
	}
	permissions := map[string]string{}
	permissions["vector_test"] = "read"
	success = CreateUser("admin", "vector_test", "vector_test_pwd", "user", permissions, globalConn, globalReader)
	if !success {
		t.Errorf("User creation failed")
	}

	conn, err := net.Dial("tcp", "localhost:4444")
	if err != nil {
		t.Errorf("TCP error")
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	success = Login("vector_test", "vector_test_pwd", conn, reader)

	if !success {
		t.Errorf("Vector test Login failed")
	}

	// Test SEARCH_TOPK with read permission (should succeed)
	query := models.Query{Type: "SEARCH_TOPK", Value: "0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8,0.9,1.0", Space: "vector_test", User: "vector_test", Dimension: 5}
	data, _ := json.Marshal(query)
	conn.Write(append(data, '\n'))

	resp, err := reader.ReadString('\n')
	if err != nil {
		t.Errorf("Server error")
		fmt.Println("Server response error:", err)
	}

	if strings.Contains(resp, `permission denied`) {
		t.Errorf("Unexpected permission denied for SEARCH_TOPK with read permission")
	}

	// Test GET_VECTOR with read permission (should succeed)
	query = models.Query{Type: "GET_VECTOR", Key: "1", Space: "vector_test", User: "vector_test"}
	data, _ = json.Marshal(query)
	conn.Write(append(data, '\n'))

	resp, err = reader.ReadString('\n')
	if err != nil {
		t.Errorf("Server error")
		fmt.Println("Server response error:", err)
	}

	if strings.Contains(resp, `permission denied`) {
		t.Errorf("Unexpected permission denied for GET_VECTOR with read permission")
	}

	// Test INSERT_VECTOR with read permission (should fail)
	query = models.Query{Type: "INSERT_VECTOR", Key: "1", Value: "0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8,0.9,1.0", Space: "vector_test", User: "vector_test"}
	data, _ = json.Marshal(query)
	conn.Write(append(data, '\n'))

	resp, err = reader.ReadString('\n')
	fmt.Println("Server response:", resp)
	if err != nil {
		t.Errorf("Server error")
		fmt.Println("Server response error:", err)
	}

	if !strings.Contains(resp, `permission denied`) {
		t.Errorf("Expected permission denied for INSERT_VECTOR with read permission")
	}
}

func TestVectorSpaceWriteAccess(t *testing.T) {
	CleanSpace("vector_test_write", globalConn, globalReader)
	CleanUser("vector_test_write", globalConn, globalReader)
	success := CreateVectorSpace("vector_test_write", 128, "Flat", "L2", globalConn, globalReader)
	if !success {
		t.Errorf("Vector space creation failed")
	}
	permissions := map[string]string{}
	permissions["vector_test_write"] = "write"
	success = CreateUser("admin", "vector_test_write", "vector_test_write_pwd", "user", permissions, globalConn, globalReader)
	if !success {
		t.Errorf("User creation failed")
	}

	conn, err := net.Dial("tcp", "localhost:4444")
	if err != nil {
		t.Errorf("TCP error")
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	success = Login("vector_test_write", "vector_test_write_pwd", conn, reader)

	if !success {
		t.Errorf("Vector test write Login failed")
	}

	// Test INSERT_VECTOR with write permission (should succeed)
	query := models.Query{Type: "INSERT_VECTOR", Key: "1", Value: "0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8,0.9,1.0", Space: "vector_test_write", User: "vector_test_write"}
	data, _ := json.Marshal(query)
	conn.Write(append(data, '\n'))

	resp, err := reader.ReadString('\n')
	if err != nil {
		t.Errorf("Server error")
		fmt.Println("Server response error:", err)
	}

	if strings.Contains(resp, `permission denied`) {
		t.Errorf("Unexpected permission denied for INSERT_VECTOR with write permission")
	}

	// Test SEARCH_TOPK with write permission (should succeed)
	query = models.Query{Type: "SEARCH_TOPK", Value: "0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8,0.9,1.0", Space: "vector_test_write", User: "vector_test_write", Dimension: 5}
	data, _ = json.Marshal(query)
	conn.Write(append(data, '\n'))

	resp, err = reader.ReadString('\n')
	if err != nil {
		t.Errorf("Server error")
		fmt.Println("Server response error:", err)
	}

	if strings.Contains(resp, `permission denied`) {
		t.Errorf("Unexpected permission denied for SEARCH_TOPK with write permission")
	}

	// Test GET_VECTOR with write permission (should succeed)
	query = models.Query{Type: "GET_VECTOR", Key: "1", Space: "vector_test_write", User: "vector_test_write"}
	data, _ = json.Marshal(query)
	conn.Write(append(data, '\n'))

	resp, err = reader.ReadString('\n')
	if err != nil {
		t.Errorf("Server error")
		fmt.Println("Server response error:", err)
	}

	if strings.Contains(resp, `permission denied`) {
		t.Errorf("Unexpected permission denied for GET_VECTOR with write permission")
	}
}

func TestVectorSpaceAdminAccess(t *testing.T) {
	CleanSpace("vector_test_admin", globalConn, globalReader)
	CleanUser("vector_test_admin", globalConn, globalReader)
	success := CreateVectorSpace("vector_test_admin", 128, "Flat", "L2", globalConn, globalReader)
	if !success {
		t.Errorf("Vector space creation failed")
	}
	permissions := map[string]string{}
	permissions["vector_test_admin"] = "admin"
	success = CreateUser("admin", "vector_test_admin", "vector_test_admin_pwd", "admin", permissions, globalConn, globalReader)
	if !success {
		t.Errorf("User creation failed")
	}

	conn, err := net.Dial("tcp", "localhost:4444")
	if err != nil {
		t.Errorf("TCP error")
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	success = Login("vector_test_admin", "vector_test_admin_pwd", conn, reader)

	if !success {
		t.Errorf("Vector test admin Login failed")
	}

	// Test INSERT_VECTOR with admin permission (should succeed)
	query := models.Query{Type: "INSERT_VECTOR", Key: "1", Value: "0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8,0.9,1.0", Space: "vector_test_admin", User: "vector_test_admin"}
	data, _ := json.Marshal(query)
	conn.Write(append(data, '\n'))

	resp, err := reader.ReadString('\n')
	if err != nil {
		t.Errorf("Server error")
		fmt.Println("Server response error:", err)
	}

	if strings.Contains(resp, `permission denied`) {
		t.Errorf("Unexpected permission denied for INSERT_VECTOR with admin permission")
	}

	// Test SEARCH_TOPK with admin permission (should succeed)
	query = models.Query{Type: "SEARCH_TOPK", Value: "0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8,0.9,1.0", Space: "vector_test_admin", User: "vector_test_admin", Dimension: 5}
	data, _ = json.Marshal(query)
	conn.Write(append(data, '\n'))

	resp, err = reader.ReadString('\n')
	if err != nil {
		t.Errorf("Server error")
		fmt.Println("Server response error:", err)
	}

	if strings.Contains(resp, `permission denied`) {
		t.Errorf("Unexpected permission denied for SEARCH_TOPK with admin permission")
	}

	// Test GET_VECTOR with admin permission (should succeed)
	query = models.Query{Type: "GET_VECTOR", Key: "1", Space: "vector_test_admin", User: "vector_test_admin"}
	data, _ = json.Marshal(query)
	conn.Write(append(data, '\n'))

	resp, err = reader.ReadString('\n')
	if err != nil {
		t.Errorf("Server error")
		fmt.Println("Server response error:", err)
	}

	if strings.Contains(resp, `permission denied`) {
		t.Errorf("Unexpected permission denied for GET_VECTOR with admin permission")
	}
}

func TestListUsersAdminSuccess(t *testing.T) {
	CleanUser("lu_user1", globalConn, globalReader)
	CleanUser("lu_user2", globalConn, globalReader)

	perms1 := map[string]string{"myspace": "write", "other": "read"}
	if !CreateUser("admin", "lu_user1", "lu_pass1", "user", perms1, globalConn, globalReader) {
		t.Fatal("failed to create lu_user1")
	}
	perms2 := map[string]string{}
	if !CreateUser("admin", "lu_user2", "lu_pass2", "user", perms2, globalConn, globalReader) {
		t.Fatal("failed to create lu_user2")
	}

	query := models.Query{Type: models.TypeListUsers, User: "admin"}
	data, _ := json.Marshal(query)
	globalConn.Write(append(data, '\n'))
	resp, err := globalReader.ReadString('\n')
	if err != nil {
		t.Fatalf("server error: %v", err)
	}

	if !strings.Contains(resp, `"status":"OK"`) {
		t.Fatalf("expected OK, got: %s", strings.TrimSpace(resp))
	}
	if !strings.Contains(resp, `"users"`) {
		t.Errorf("response missing 'users' key: %s", strings.TrimSpace(resp))
	}
	if strings.Contains(resp, `"password"`) {
		t.Errorf("response must not include password field: %s", strings.TrimSpace(resp))
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(resp)), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	list, ok := parsed["users"].([]interface{})
	if !ok {
		t.Fatalf("'users' is not an array")
	}

	found1, found2 := false, false
	for _, u := range list {
		m, ok := u.(map[string]interface{})
		if !ok {
			continue
		}
		if _, hasPass := m["password"]; hasPass {
			t.Errorf("user entry contains password field")
		}
		switch m["username"] {
		case "lu_user1":
			found1 = true
			if m["role"] != "user" {
				t.Errorf("lu_user1: expected role=user, got %v", m["role"])
			}
			if perms, ok := m["permissions"].(map[string]interface{}); ok {
				if perms["myspace"] != "write" || perms["other"] != "read" {
					t.Errorf("lu_user1: unexpected permissions: %v", perms)
				}
			} else {
				t.Errorf("lu_user1: permissions not a map")
			}
		case "lu_user2":
			found2 = true
		}
	}
	if !found1 {
		t.Errorf("lu_user1 not found in list response")
	}
	if !found2 {
		t.Errorf("lu_user2 not found in list response")
	}
}

func TestListUsersNonAdminDenied(t *testing.T) {
	CleanUser("lu_nonadmin", globalConn, globalReader)
	if !CreateUser("admin", "lu_nonadmin", "lu_nonadmin_pass", "user", map[string]string{}, globalConn, globalReader) {
		t.Fatal("failed to create lu_nonadmin")
	}

	conn, err := net.Dial("tcp", "localhost:4444")
	if err != nil {
		t.Fatalf("TCP error: %v", err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)

	if !Login("lu_nonadmin", "lu_nonadmin_pass", conn, reader) {
		t.Fatal("login failed for lu_nonadmin")
	}

	query := models.Query{Type: models.TypeListUsers, User: "lu_nonadmin"}
	data, _ := json.Marshal(query)
	conn.Write(append(data, '\n'))
	resp, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("server error: %v", err)
	}

	if !strings.Contains(resp, `"ERROR"`) {
		t.Errorf("expected error for non-admin LIST_USERS, got: %s", strings.TrimSpace(resp))
	}
	if strings.Contains(resp, `"users"`) {
		t.Errorf("non-admin must not receive users list: %s", strings.TrimSpace(resp))
	}
}

func TestVectorSpaceNoAccess(t *testing.T) {
	CleanSpace("vector_test_no_access", globalConn, globalReader)
	CleanUser("vector_test_no_access", globalConn, globalReader)
	success := CreateVectorSpace("vector_test_no_access", 128, "Flat", "L2", globalConn, globalReader)
	if !success {
		t.Errorf("Vector space creation failed")
	}
	// Create user with no permissions for this space
	permissions := map[string]string{}
	permissions["other_space"] = "read" // Different space
	success = CreateUser("admin", "vector_test_no_access", "vector_test_no_access_pwd", "user", permissions, globalConn, globalReader)
	if !success {
		t.Errorf("User creation failed")
	}

	conn, err := net.Dial("tcp", "localhost:4444")
	if err != nil {
		t.Errorf("TCP error")
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	success = Login("vector_test_no_access", "vector_test_no_access_pwd", conn, reader)

	if !success {
		t.Errorf("Vector test no access Login failed")
	}

	// Test INSERT_VECTOR with no permission (should fail)
	query := models.Query{Type: "INSERT_VECTOR", Key: "1", Value: "0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8,0.9,1.0", Space: "vector_test_no_access", User: "vector_test_no_access"}
	data, _ := json.Marshal(query)
	conn.Write(append(data, '\n'))

	resp, err := reader.ReadString('\n')
	if err != nil {
		t.Errorf("Server error")
		fmt.Println("Server response error:", err)
	}

	if !strings.Contains(resp, `permission denied`) {
		t.Errorf("Expected permission denied for INSERT_VECTOR with no permission")
	}

	// Test SEARCH_TOPK with no permission (should fail)
	query = models.Query{Type: "SEARCH_TOPK", Value: "0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8,0.9,1.0", Space: "vector_test_no_access", User: "vector_test_no_access", Dimension: 5}
	data, _ = json.Marshal(query)
	conn.Write(append(data, '\n'))

	resp, err = reader.ReadString('\n')
	if err != nil {
		t.Errorf("Server error")
		fmt.Println("Server response error:", err)
	}

	if !strings.Contains(resp, `permission denied`) {
		t.Errorf("Expected permission denied for SEARCH_TOPK with no permission")
	}

	// Test GET_VECTOR with no permission (should fail)
	query = models.Query{Type: "GET_VECTOR", Key: "1", Space: "vector_test_no_access", User: "vector_test_no_access"}
	data, _ = json.Marshal(query)
	conn.Write(append(data, '\n'))

	resp, err = reader.ReadString('\n')
	if err != nil {
		t.Errorf("Server error")
		fmt.Println("Server response error:", err)
	}

	if !strings.Contains(resp, `permission denied`) {
		t.Errorf("Expected permission denied for GET_VECTOR with no permission")
	}
}

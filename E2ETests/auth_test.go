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

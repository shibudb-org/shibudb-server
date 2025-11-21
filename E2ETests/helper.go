package E2ETests

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/shibudb.org/shibudb-server/internal/models"
)

func SendQuery(q models.Query, conn net.Conn, reader *bufio.Reader) error {
	data, _ := json.Marshal(q)
	_, err := conn.Write(append(data, '\n'))
	if err != nil {
		return err
	}
	_, err = reader.ReadBytes('\n')
	return err
}

func CreateSpaceWithIndex(tableSpace, engineType string, dimension int, indexType string, metric string, conn net.Conn, reader *bufio.Reader) bool {
	query := models.Query{Type: "CREATE_SPACE", Space: tableSpace, EngineType: engineType, Dimension: dimension, IndexType: indexType, Metric: metric, EnableWAL: true}
	data, _ := json.Marshal(query)
	_, err := conn.Write(append(data, '\n'))
	if err != nil {
		return false
	}
	resp, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(resp, "\"status\":\"OK\"") && !strings.Contains(resp, "SPACE_CREATED") {
		fmt.Println("Table space creation failed. Server response:", strings.TrimSpace(resp))
		return false
	}
	return true
}

func CleanSpace(tableSpace string, conn net.Conn, reader *bufio.Reader) {
	query := models.Query{Type: models.TypeDeleteSpace, Data: tableSpace}
	data, _ := json.Marshal(query)
	_, err := conn.Write(append(data, '\n'))
	if err != nil {
		return
	}
	reader.ReadString('\n')
}

func CleanUser(username string, conn net.Conn, reader *bufio.Reader) {
	userToDelete := models.User{
		Username: username,
	}
	query := models.Query{Type: models.TypeDeleteUser, DeleteUser: &userToDelete}
	data, _ := json.Marshal(query)
	_, err := conn.Write(append(data, '\n'))
	if err != nil {
		return
	}
	reader.ReadString('\n')
}

func CreateUser(currentUser string, username string, password string, role string, permissions map[string]string, conn net.Conn, reader *bufio.Reader) bool {
	newUser := models.User{
		Username:    username,
		Password:    password,
		Role:        role,
		Permissions: permissions,
	}
	query := models.Query{
		Type:    models.TypeCreateUser,
		User:    currentUser,
		NewUser: &newUser,
	}
	data, _ := json.Marshal(query)
	_, err := conn.Write(append(data, '\n'))
	if err != nil {
		return false
	}
	resp, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(resp, `"status":"OK"`) {
		fmt.Println("User creation failed. Server response:", strings.TrimSpace(resp))
		return false
	}
	return true
}

func Login(username string, password string, conn net.Conn, reader *bufio.Reader) bool {
	login := models.LoginRequest{Username: username, Password: password}
	data, _ := json.Marshal(login)
	conn.Write(append(data, '\n'))

	resp, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(resp, `"status":"OK"`) {
		fmt.Println("Authentication failed. Server response:", strings.TrimSpace(resp))
		return false
	}

	return true
}

func CreateVectorSpace(space string, dimension int, indexType string, metric string, conn net.Conn, reader *bufio.Reader) bool {
	query := models.Query{
		Type:       "CREATE_SPACE",
		Space:      space,
		EngineType: "vector",
		Dimension:  dimension,
		IndexType:  indexType,
		Metric:     metric,
		EnableWAL:  true,
	}
	data, _ := json.Marshal(query)
	_, err := conn.Write(append(data, '\n'))
	if err != nil {
		return false
	}
	resp, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(resp, "\"status\":\"OK\"") && !strings.Contains(resp, "SPACE_CREATED") {
		fmt.Println("Vector space creation failed. Server response:", strings.TrimSpace(resp))
		return false
	}
	return true
}

func formatVec(vec []float32) string {
	out := ""
	for i, v := range vec {
		if i > 0 {
			out += ","
		}
		out += strconv.FormatFloat(float64(v), 'f', -1, 32)
	}
	return out
}

func formatID(id int64) string {
	return strconv.FormatInt(id, 10)
}

func sendQueryAndGetResponse(q models.Query, conn net.Conn, reader *bufio.Reader) string {
	data, _ := json.Marshal(q)
	conn.Write(append(data, '\n'))
	resp, _ := reader.ReadString('\n')
	return resp
}

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/shibudb.org/shibudb-server/internal/models"
)

func main() {
	conn, err := net.Dial("tcp", "localhost:9090")
	if err != nil {
		fmt.Printf("Failed to connect to server: %v\n", err)
		return
	}
	defer conn.Close()

	reader := bufio.NewReader(os.Stdin)
	serverReader := bufio.NewReader(conn)

	fmt.Println("Connected to ShibuDB. Use: put/get/delete <key> [value], or type 'quit' to exit.")

	for {
		fmt.Print("> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println("Input error:", err)
			continue
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Exit condition
		if strings.EqualFold(line, "quit") || strings.EqualFold(line, "exit") {
			fmt.Println("Goodbye!")
			break
		}

		parts := strings.Fields(line)
		if len(parts) < 2 {
			fmt.Println("Usage: put/get/delete <key> [value]")
			continue
		}

		var query models.Query
		switch strings.ToLower(parts[0]) {
		case "put":
			if len(parts) < 3 {
				fmt.Println("Usage: put <key> <value>")
				continue
			}
			query = models.Query{Type: models.TypePut, Key: parts[1], Value: parts[2]}
		case "get":
			query = models.Query{Type: models.TypeGet, Key: parts[1]}
		case "delete":
			query = models.Query{Type: models.TypeDelete, Key: parts[1]}
		default:
			fmt.Println("Unknown command:", parts[0])
			continue
		}

		data, _ := json.Marshal(query)
		conn.Write(append(data, '\n'))

		resp, err := serverReader.ReadString('\n')
		if err != nil {
			fmt.Println("Server response error:", err)
			break
		}

		fmt.Println(strings.TrimSpace(resp))
	}
}

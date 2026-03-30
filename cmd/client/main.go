package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/shibudb.org/shibudb-server/internal/cliinput"
	"github.com/shibudb.org/shibudb-server/internal/models"
)

func main() {
	conn, err := net.Dial("tcp", "localhost:4444")
	if err != nil {
		fmt.Printf("Failed to connect to server: %v\n", err)
		return
	}
	defer conn.Close()

	input, err := cliinput.New(os.Stdin, os.Stdout)
	if err != nil {
		fmt.Printf("Failed to initialize CLI input: %v\n", err)
		return
	}
	defer input.Close()

	serverReader := bufio.NewReader(conn)

	fmt.Println("Connected to ShibuDB. Use: put/get/delete <key> [value], or type 'quit' to exit.")

	for {
		line, err := input.ReadLine("> ")
		if err != nil {
			fmt.Println("Input error:", err)
			break
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		input.AppendHistory(line)

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

		if err := sendJSONLine(conn, query); err != nil {
			fmt.Println("Request error:", err)
			break
		}

		resp, err := serverReader.ReadString('\n')
		if err != nil {
			fmt.Println("Server response error:", err)
			break
		}

		fmt.Println(strings.TrimSpace(resp))
	}
}

func sendJSONLine(conn net.Conn, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request payload: %w", err)
	}

	if _, err := conn.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write request payload: %w", err)
	}

	return nil
}

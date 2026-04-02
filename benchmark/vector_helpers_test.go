package benchmark

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/shibudb.org/shibudb-server/internal/models"
)

type benchmarkResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Value   string `json:"value,omitempty"`
}

func sendCheckedVectorQuery(q models.Query, conn net.Conn, reader *bufio.Reader) (benchmarkResponse, error) {
	var respObj benchmarkResponse

	data, err := json.Marshal(q)
	if err != nil {
		return respObj, err
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return respObj, err
	}

	resp, err := reader.ReadBytes('\n')
	if err != nil {
		return respObj, err
	}
	if err := json.Unmarshal(resp, &respObj); err != nil {
		return respObj, err
	}
	if respObj.Status != "OK" {
		if respObj.Message == "" {
			return respObj, fmt.Errorf("%s failed with status %s", q.Type, respObj.Status)
		}
		return respObj, fmt.Errorf("%s failed: %s", q.Type, respObj.Message)
	}

	return respObj, nil
}

func getVectorEventually(q models.Query, conn net.Conn, reader *bufio.Reader) (benchmarkResponse, error) {
	var (
		resp benchmarkResponse
		err  error
	)

	for attempt := 0; attempt < 10; attempt++ {
		resp, err = sendCheckedVectorQuery(q, conn, reader)
		if err == nil {
			return resp, nil
		}
		time.Sleep(20 * time.Millisecond)
	}

	return resp, err
}

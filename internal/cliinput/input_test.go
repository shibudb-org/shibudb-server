package cliinput

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestBufferedReaderReadLineWritesPrompt(t *testing.T) {
	stdout := &bytes.Buffer{}
	reader := &bufferedReader{
		reader: bufio.NewReader(strings.NewReader("put key value\n")),
		writer: stdout,
	}

	line, err := reader.ReadLine("> ")
	if err != nil {
		t.Fatalf("ReadLine returned error: %v", err)
	}

	if line != "put key value" {
		t.Fatalf("expected line %q, got %q", "put key value", line)
	}

	if stdout.String() != "> " {
		t.Fatalf("expected prompt %q, got %q", "> ", stdout.String())
	}
}

func TestBufferedReaderReadLineHandlesEOFWithoutTrailingNewline(t *testing.T) {
	stdout := &bytes.Buffer{}
	reader := &bufferedReader{
		reader: bufio.NewReader(strings.NewReader("get key")),
		writer: stdout,
	}

	line, err := reader.ReadLine("> ")
	if err != nil {
		t.Fatalf("ReadLine returned error: %v", err)
	}

	if line != "get key" {
		t.Fatalf("expected line %q, got %q", "get key", line)
	}
}

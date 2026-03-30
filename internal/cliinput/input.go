package cliinput

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/peterh/liner"
)

type Reader interface {
	ReadLine(prompt string) (string, error)
	ReadPassword(prompt string) (string, error)
	AppendHistory(entry string)
	Close() error
}

type interactiveReader struct {
	state *liner.State
}

type bufferedReader struct {
	reader *bufio.Reader
	writer io.Writer
}

func New(stdin *os.File, stdout io.Writer) (Reader, error) {
	stdinInfo, err := stdin.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect stdin: %w", err)
	}

	if stdinInfo.Mode()&os.ModeCharDevice != 0 {
		return &interactiveReader{
			state: liner.NewLiner(),
		}, nil
	}

	return &bufferedReader{
		reader: bufio.NewReader(stdin),
		writer: stdout,
	}, nil
}

func (r *interactiveReader) ReadLine(prompt string) (string, error) {
	line, err := r.state.Prompt(prompt)
	if err != nil {
		return "", fmt.Errorf("read prompt %q: %w", prompt, err)
	}

	return line, nil
}

func (r *interactiveReader) ReadPassword(prompt string) (string, error) {
	line, err := r.state.PasswordPrompt(prompt)
	if err != nil {
		return "", fmt.Errorf("read password prompt %q: %w", prompt, err)
	}

	return line, nil
}

func (r *interactiveReader) AppendHistory(entry string) {
	if strings.TrimSpace(entry) == "" {
		return
	}

	r.state.AppendHistory(entry)
}

func (r *interactiveReader) Close() error {
	return r.state.Close()
}

func (r *bufferedReader) ReadLine(prompt string) (string, error) {
	if _, err := fmt.Fprint(r.writer, prompt); err != nil {
		return "", fmt.Errorf("write prompt %q: %w", prompt, err)
	}

	return readBufferedLine(r.reader, prompt)
}

func (r *bufferedReader) ReadPassword(prompt string) (string, error) {
	return r.ReadLine(prompt)
}

func (r *bufferedReader) AppendHistory(entry string) {}

func (r *bufferedReader) Close() error {
	return nil
}

func readBufferedLine(reader *bufio.Reader, prompt string) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		if len(line) == 0 {
			return "", fmt.Errorf("read prompt %q: %w", prompt, err)
		}

		return strings.TrimRight(line, "\r\n"), nil
	}

	return strings.TrimRight(line, "\r\n"), nil
}

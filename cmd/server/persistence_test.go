package server

import (
	"os"
	"testing"
)

func TestLoadConnectionLimit_missingFileIsNotExist(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadConnectionLimit(dir)
	if err == nil || !os.IsNotExist(err) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
}

func TestGetPersistentLimit_missingFileUsesFallback(t *testing.T) {
	dir := t.TempDir()
	const fallback int32 = 42
	if got := GetPersistentLimit(dir, fallback); got != fallback {
		t.Fatalf("got %d want %d", got, fallback)
	}
}

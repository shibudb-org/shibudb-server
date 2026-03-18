package main

import (
	"reflect"
	"testing"

	"github.com/shibudb.org/shibudb-server/cmd/server"
)

func TestNormalizeListenPort(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		got, err := normalizeListenPort(" 9090 ")
		if err != nil || got != "9090" {
			t.Fatalf("got %q err %v", got, err)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if _, err := normalizeListenPort(""); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("out of range", func(t *testing.T) {
		if _, err := normalizeListenPort("0"); err == nil {
			t.Fatal("expected error")
		}
		if _, err := normalizeListenPort("65536"); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("non numeric", func(t *testing.T) {
		if _, err := normalizeListenPort("abc"); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestResolveDefaultMaxConnections(t *testing.T) {
	t.Run("empty env uses DefaultMaxConnections", func(t *testing.T) {
		t.Setenv("SHIBUDB_MAX_CONNECTIONS", "")
		if got := resolveDefaultMaxConnections(); got != server.DefaultMaxConnections {
			t.Fatalf("got %d want %d", got, server.DefaultMaxConnections)
		}
	})
	t.Run("valid env", func(t *testing.T) {
		t.Setenv("SHIBUDB_MAX_CONNECTIONS", "2048")
		if got := resolveDefaultMaxConnections(); got != 2048 {
			t.Fatalf("got %d want 2048", got)
		}
	})
	t.Run("invalid env falls back", func(t *testing.T) {
		t.Setenv("SHIBUDB_MAX_CONNECTIONS", "x")
		if got := resolveDefaultMaxConnections(); got != server.DefaultMaxConnections {
			t.Fatalf("got %d want %d", got, server.DefaultMaxConnections)
		}
	})
	t.Run("whitespace trimmed valid", func(t *testing.T) {
		t.Setenv("SHIBUDB_MAX_CONNECTIONS", "  3000  ")
		if got := resolveDefaultMaxConnections(); got != 3000 {
			t.Fatalf("got %d want 3000", got)
		}
	})
	t.Run("non-positive env falls back", func(t *testing.T) {
		t.Setenv("SHIBUDB_MAX_CONNECTIONS", "0")
		if got := resolveDefaultMaxConnections(); got != server.DefaultMaxConnections {
			t.Fatalf("got %d want %d", got, server.DefaultMaxConnections)
		}
	})
}

func TestBuildRunSubcommandArgs(t *testing.T) {
	root := t.TempDir()
	paths := newRuntimePaths(root)
	defPort := server.DefaultPort
	defMgmt := server.DefaultManagementPort
	def := int32(1000)

	t.Run("defaults omit port and max flags", func(t *testing.T) {
		got := buildRunSubcommandArgs(defPort, defPort, defMgmt, defMgmt, def, def, paths, "", "")
		want := []string{"run", "--data-dir", root}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v want %#v", got, want)
		}
	})
	t.Run("non-default port", func(t *testing.T) {
		got := buildRunSubcommandArgs("9090", defPort, defMgmt, defMgmt, def, def, paths, "", "")
		want := []string{"run", "--data-dir", root, "--port", "9090"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v want %#v", got, want)
		}
	})
	t.Run("non-default management-port", func(t *testing.T) {
		got := buildRunSubcommandArgs(defPort, defPort, "7000", defMgmt, def, def, paths, "", "")
		want := []string{"run", "--data-dir", root, "--management-port", "7000"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v want %#v", got, want)
		}
	})
	t.Run("non-default max-connections", func(t *testing.T) {
		got := buildRunSubcommandArgs(defPort, defPort, defMgmt, defMgmt, 500, def, paths, "", "")
		want := []string{"run", "--data-dir", root, "--max-connections", "500"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v want %#v", got, want)
		}
	})
	t.Run("with admin bootstrap", func(t *testing.T) {
		got := buildRunSubcommandArgs(defPort, defPort, defMgmt, defMgmt, def, def, paths, "admin", "secret")
		want := []string{"run", "--data-dir", root, "--admin-user", "admin", "--admin-password", "secret"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v want %#v", got, want)
		}
	})
}

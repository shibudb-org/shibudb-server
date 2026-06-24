package cliparse

import (
	"reflect"
	"testing"
)

func TestTokenize(t *testing.T) {
	cases := []struct {
		name string
		line string
		want []string
	}{
		{"empty", "", nil},
		{"only spaces", "   ", nil},
		{"single token", "get", []string{"get"}},
		{"simple command", "put userName John", []string{"put", "userName", "John"}},
		{
			name: "unquoted multi-word value",
			line: "put userName John Doe",
			want: []string{"put", "userName", "John", "Doe"},
		},
		{
			name: "double-quoted value with space stays one token",
			line: `put userName "John Doe"`,
			want: []string{"put", "userName", "John Doe"},
		},
		{
			name: "single-quoted value with space stays one token",
			line: "put userName 'John Doe'",
			want: []string{"put", "userName", "John Doe"},
		},
		{
			name: "double quotes preserve internal multiple spaces",
			line: `put k "a    b"`,
			want: []string{"put", "k", "a    b"},
		},
		{
			name: "double quotes may contain single quotes",
			line: `put k "it's fine"`,
			want: []string{"put", "k", "it's fine"},
		},
		{
			name: "single quotes may contain double quotes",
			line: `put k 'say "hi"'`,
			want: []string{"put", "k", `say "hi"`},
		},
		{
			name: "empty quoted value is a real empty token",
			line: `put k ""`,
			want: []string{"put", "k", ""},
		},
		{
			name: "leading and trailing whitespace ignored",
			line: "  put   userName   John  ",
			want: []string{"put", "userName", "John"},
		},
		{
			name: "tabs treated as separators",
			line: "put\tuserName\tJohn",
			want: []string{"put", "userName", "John"},
		},
		{
			name: "unterminated quote takes the rest of the line",
			line: `put userName "John Doe`,
			want: []string{"put", "userName", "John Doe"},
		},
		{
			name: "quoted value preserves leading and trailing spaces",
			line: `put k " padded "`,
			want: []string{"put", "k", " padded "},
		},
		{
			name: "flags on other commands are unaffected",
			line: "create-space vectors --engine vector --dimension 128",
			want: []string{"create-space", "vectors", "--engine", "vector", "--dimension", "128"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Tokenize(tc.line)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Tokenize(%q) = %#v, want %#v", tc.line, got, tc.want)
			}
		})
	}
}

func TestPutValue(t *testing.T) {
	cases := []struct {
		name   string
		tokens []string
		want   string
	}{
		{"missing value", []string{"put", "key"}, ""},
		{"single word", []string{"put", "key", "John"}, "John"},
		{
			name:   "quoted value already merged",
			tokens: []string{"put", "key", "John Doe"},
			want:   "John Doe",
		},
		{
			name:   "unquoted words rejoined",
			tokens: []string{"put", "key", "John", "Doe"},
			want:   "John Doe",
		},
		{"empty value token", []string{"put", "key", ""}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PutValue(tc.tokens); got != tc.want {
				t.Fatalf("PutValue(%#v) = %q, want %q", tc.tokens, got, tc.want)
			}
		})
	}
}

func TestPutRoundTrip(t *testing.T) {
	cases := []struct {
		line      string
		wantKey   string
		wantValue string
	}{
		{"put userName John Doe", "userName", "John Doe"},
		{`put userName "John Doe"`, "userName", "John Doe"},
		{"put userName 'John Doe'", "userName", "John Doe"},
		{"put userName John", "userName", "John"},
		{`put greeting "hello, world"`, "greeting", "hello, world"},
	}

	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			parts := Tokenize(tc.line)
			if len(parts) < 3 {
				t.Fatalf("Tokenize(%q) produced %d tokens, want >= 3", tc.line, len(parts))
			}
			if parts[1] != tc.wantKey {
				t.Fatalf("key = %q, want %q", parts[1], tc.wantKey)
			}
			if got := PutValue(parts); got != tc.wantValue {
				t.Fatalf("value = %q, want %q", got, tc.wantValue)
			}
		})
	}
}

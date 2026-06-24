package cliparse

import "strings"

func Tokenize(line string) []string {
	var tokens []string
	var cur strings.Builder
	inToken := false
	var quote rune // 0 when outside quotes, else the active quote character

	flush := func() {
		if inToken {
			tokens = append(tokens, cur.String())
			cur.Reset()
			inToken = false
		}
	}

	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
			inToken = true
		case r == '\'' || r == '"':
			quote = r
			inToken = true
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
			inToken = true
		}
	}
	flush()
	return tokens
}

func PutValue(tokens []string) string {
	if len(tokens) < 3 {
		return ""
	}
	return strings.Join(tokens[2:], " ")
}

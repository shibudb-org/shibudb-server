package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/shibudb.org/shibudb-server/internal/storage"
)

// This file implements the client-side parsing helpers that translate friendly
// CLI syntax into the wire-protocol fields understood by the server:
//
//	--metadata-fields user_id:string,price:float,year:int   -> []storage.MetadataFieldSpec
//	--meta user_id=alice,price=12.5,year=2020               -> map[string]any
//	--where "<expression>"                                  -> *storage.MetadataFilter
//
// The --where grammar (case-insensitive keywords):
//
//	expr      := orExpr
//	orExpr    := andExpr (OR andExpr)*
//	andExpr   := notExpr (AND notExpr)*
//	notExpr   := NOT notExpr | primary
//	primary   := '(' expr ')' | predicate
//	predicate := field op value
//	           | field IN '(' value (',' value)* ')'
//	           | field BETWEEN value AND value
//	op        := '=' | '==' | '!=' | '>' | '>=' | '<' | '<='
//
// Values are quoted strings ('x' or "x"), bare words (treated as strings), or
// numbers. '!=' is expressed as NOT(field = value).

// splitTrailingFlag finds flag as a whitespace-delimited token in line and
// returns the text before it, the text after it (trimmed), and whether it was
// found. If not found, base is the original line and found is false.
func splitTrailingFlag(line, flag string) (base, value string, found bool) {
	idx := strings.Index(line, flag)
	for idx >= 0 {
		beforeOK := idx == 0 || line[idx-1] == ' ' || line[idx-1] == '\t'
		afterPos := idx + len(flag)
		afterOK := afterPos >= len(line) || line[afterPos] == ' ' || line[afterPos] == '\t'
		if beforeOK && afterOK {
			return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[afterPos:]), true
		}
		rel := strings.Index(line[idx+1:], flag)
		if rel < 0 {
			break
		}
		idx = idx + 1 + rel
	}
	return line, "", false
}

func parseMetadataFields(spec string) ([]storage.MetadataFieldSpec, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	var specs []storage.MetadataFieldSpec
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid field %q (expected name:type)", part)
		}
		name := strings.TrimSpace(kv[0])
		typ := strings.TrimSpace(kv[1])
		if name == "" || typ == "" {
			return nil, fmt.Errorf("invalid field %q (expected name:type)", part)
		}
		specs = append(specs, storage.MetadataFieldSpec{Name: name, Type: typ})
	}
	return specs, nil
}

func parseMetaValues(spec string) (map[string]any, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	meta := make(map[string]any)
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid metadata %q (expected key=value)", part)
		}
		key := strings.TrimSpace(kv[0])
		raw := strings.TrimSpace(kv[1])
		if key == "" {
			return nil, fmt.Errorf("invalid metadata %q (empty key)", part)
		}
		meta[key] = inferValue(raw)
	}
	return meta, nil
}

// inferValue converts a bare CLI token to a typed value: quoted -> string,
// numeric -> float64, otherwise -> string.
func inferValue(raw string) any {
	if len(raw) >= 2 {
		if (raw[0] == '\'' && raw[len(raw)-1] == '\'') || (raw[0] == '"' && raw[len(raw)-1] == '"') {
			return raw[1 : len(raw)-1]
		}
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return f
	}
	return raw
}

// === --where expression parser ===

type whereTokenKind int

const (
	wtIdent whereTokenKind = iota
	wtString
	wtNumber
	wtOp
	wtLParen
	wtRParen
	wtComma
	wtAnd
	wtOr
	wtNot
	wtIn
	wtBetween
	wtEOF
)

type whereToken struct {
	kind whereTokenKind
	text string
	num  float64
}

func (t whereToken) display() string {
	if t.kind == wtEOF {
		return "end of input"
	}
	return t.text
}

func tokenizeWhere(s string) ([]whereToken, error) {
	isSpecial := func(b byte) bool {
		switch b {
		case '(', ')', ',', '=', '<', '>', '!', '\'', '"':
			return true
		}
		return false
	}

	var toks []whereToken
	i, n := 0, len(s)
	for i < n {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
			continue
		case c == '(':
			toks = append(toks, whereToken{kind: wtLParen, text: "("})
			i++
			continue
		case c == ')':
			toks = append(toks, whereToken{kind: wtRParen, text: ")"})
			i++
			continue
		case c == ',':
			toks = append(toks, whereToken{kind: wtComma, text: ","})
			i++
			continue
		case c == '=':
			if i+1 < n && s[i+1] == '=' {
				i += 2
			} else {
				i++
			}
			toks = append(toks, whereToken{kind: wtOp, text: "="})
			continue
		case c == '!':
			if i+1 < n && s[i+1] == '=' {
				toks = append(toks, whereToken{kind: wtOp, text: "!="})
				i += 2
				continue
			}
			return nil, fmt.Errorf("unexpected '!' (did you mean '!='?)")
		case c == '<':
			if i+1 < n && s[i+1] == '=' {
				toks = append(toks, whereToken{kind: wtOp, text: "<="})
				i += 2
			} else {
				toks = append(toks, whereToken{kind: wtOp, text: "<"})
				i++
			}
			continue
		case c == '>':
			if i+1 < n && s[i+1] == '=' {
				toks = append(toks, whereToken{kind: wtOp, text: ">="})
				i += 2
			} else {
				toks = append(toks, whereToken{kind: wtOp, text: ">"})
				i++
			}
			continue
		case c == '\'' || c == '"':
			quote := c
			i++
			var sb strings.Builder
			for i < n && s[i] != quote {
				if s[i] == '\\' && i+1 < n {
					sb.WriteByte(s[i+1])
					i += 2
					continue
				}
				sb.WriteByte(s[i])
				i++
			}
			if i >= n {
				return nil, fmt.Errorf("unterminated string literal")
			}
			i++ // consume closing quote
			toks = append(toks, whereToken{kind: wtString, text: sb.String()})
			continue
		}

		start := i
		for i < n {
			b := s[i]
			if b == ' ' || b == '\t' || b == '\n' || b == '\r' || isSpecial(b) {
				break
			}
			i++
		}
		word := s[start:i]
		switch strings.ToUpper(word) {
		case "AND":
			toks = append(toks, whereToken{kind: wtAnd, text: "AND"})
		case "OR":
			toks = append(toks, whereToken{kind: wtOr, text: "OR"})
		case "NOT":
			toks = append(toks, whereToken{kind: wtNot, text: "NOT"})
		case "IN":
			toks = append(toks, whereToken{kind: wtIn, text: "IN"})
		case "BETWEEN":
			toks = append(toks, whereToken{kind: wtBetween, text: "BETWEEN"})
		default:
			if f, err := strconv.ParseFloat(word, 64); err == nil {
				toks = append(toks, whereToken{kind: wtNumber, text: word, num: f})
			} else {
				toks = append(toks, whereToken{kind: wtIdent, text: word})
			}
		}
	}
	toks = append(toks, whereToken{kind: wtEOF})
	return toks, nil
}

type whereParser struct {
	toks []whereToken
	pos  int
}

func (p *whereParser) peek() whereToken { return p.toks[p.pos] }

func (p *whereParser) next() whereToken {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func parseWhere(s string) (*storage.MetadataFilter, error) {
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("empty expression")
	}
	toks, err := tokenizeWhere(s)
	if err != nil {
		return nil, err
	}
	p := &whereParser{toks: toks}
	filter, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != wtEOF {
		return nil, fmt.Errorf("unexpected %q", p.peek().display())
	}
	return filter, nil
}

func (p *whereParser) parseExpr() (*storage.MetadataFilter, error) {
	return p.parseOr()
}

func (p *whereParser) parseOr() (*storage.MetadataFilter, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == wtOr {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = combineFilter(storage.FilterOpOr, left, right)
	}
	return left, nil
}

func (p *whereParser) parseAnd() (*storage.MetadataFilter, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == wtAnd {
		p.next()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = combineFilter(storage.FilterOpAnd, left, right)
	}
	return left, nil
}

func (p *whereParser) parseNot() (*storage.MetadataFilter, error) {
	if p.peek().kind == wtNot {
		p.next()
		sub, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &storage.MetadataFilter{Op: storage.FilterOpNot, Filters: []*storage.MetadataFilter{sub}}, nil
	}
	return p.parsePrimary()
}

func (p *whereParser) parsePrimary() (*storage.MetadataFilter, error) {
	if p.peek().kind == wtLParen {
		p.next()
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != wtRParen {
			return nil, fmt.Errorf("expected ')', got %q", p.peek().display())
		}
		p.next()
		return inner, nil
	}
	return p.parsePredicate()
}

func (p *whereParser) parsePredicate() (*storage.MetadataFilter, error) {
	fieldTok := p.next()
	if fieldTok.kind != wtIdent {
		return nil, fmt.Errorf("expected a field name, got %q", fieldTok.display())
	}
	field := fieldTok.text

	opTok := p.next()
	switch opTok.kind {
	case wtOp:
		val, err := tokenValue(p.next())
		if err != nil {
			return nil, err
		}
		switch opTok.text {
		case "=":
			return &storage.MetadataFilter{Op: storage.FilterOpEq, Field: field, Value: val}, nil
		case "!=":
			return &storage.MetadataFilter{Op: storage.FilterOpNot, Filters: []*storage.MetadataFilter{
				{Op: storage.FilterOpEq, Field: field, Value: val},
			}}, nil
		case ">":
			return &storage.MetadataFilter{Op: storage.FilterOpGt, Field: field, Value: val}, nil
		case ">=":
			return &storage.MetadataFilter{Op: storage.FilterOpGte, Field: field, Value: val}, nil
		case "<":
			return &storage.MetadataFilter{Op: storage.FilterOpLt, Field: field, Value: val}, nil
		case "<=":
			return &storage.MetadataFilter{Op: storage.FilterOpLte, Field: field, Value: val}, nil
		}
		return nil, fmt.Errorf("unsupported operator %q", opTok.text)
	case wtIn:
		if p.peek().kind != wtLParen {
			return nil, fmt.Errorf("expected '(' after IN, got %q", p.peek().display())
		}
		p.next()
		var values []any
		for {
			val, err := tokenValue(p.next())
			if err != nil {
				return nil, err
			}
			values = append(values, val)
			sep := p.next()
			if sep.kind == wtComma {
				continue
			}
			if sep.kind == wtRParen {
				break
			}
			return nil, fmt.Errorf("expected ',' or ')' in IN list, got %q", sep.display())
		}
		return &storage.MetadataFilter{Op: storage.FilterOpIn, Field: field, Values: values}, nil
	case wtBetween:
		lo, err := tokenValue(p.next())
		if err != nil {
			return nil, err
		}
		if p.peek().kind != wtAnd {
			return nil, fmt.Errorf("expected AND in BETWEEN, got %q", p.peek().display())
		}
		p.next()
		hi, err := tokenValue(p.next())
		if err != nil {
			return nil, err
		}
		return &storage.MetadataFilter{Op: storage.FilterOpBetween, Field: field, Values: []any{lo, hi}}, nil
	default:
		return nil, fmt.Errorf("expected an operator after field %q, got %q", field, opTok.display())
	}
}

func tokenValue(t whereToken) (any, error) {
	switch t.kind {
	case wtString, wtIdent:
		return t.text, nil
	case wtNumber:
		return t.num, nil
	default:
		return nil, fmt.Errorf("expected a value, got %q", t.display())
	}
}

// combineFilter merges into a flat AND/OR node when the left side already has
// the same operator, keeping the AST shallow.
func combineFilter(op string, left, right *storage.MetadataFilter) *storage.MetadataFilter {
	if left.Op == op {
		left.Filters = append(left.Filters, right)
		return left
	}
	return &storage.MetadataFilter{Op: op, Filters: []*storage.MetadataFilter{left, right}}
}

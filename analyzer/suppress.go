package analyzer

import (
	"regexp"
	"strings"
)

// ignoreTokenRe matches a directive in text already known to be a comment.
// An optional `:rule-a, rule-b` list scopes it; without one, every rule is
// suppressed.
var ignoreTokenRe = regexp.MustCompile(`(?i)sqlguard:ignore(?::\s*([a-z0-9_,\s-]+))?`)

// parseIgnoreDirective finds `sqlguard:ignore` directives in the comments of
// raw SQL. Text inside a value must never switch a rule off (#66), and
// dialects disagree about where literals and comments start, so a directive
// counts only if every reading agrees it is not inside a literal.
func parseIgnoreDirective(sql string) (ignoreAll bool, ignored map[string]bool) {
	if !strings.Contains(strings.ToLower(sql), "sqlguard:ignore") {
		return false, nil
	}
	first := true
	for _, backslash := range []bool{false, true} {
		if backslash && strings.IndexByte(sql, '\\') < 0 {
			continue
		}
		for _, dollar := range []bool{true, false} {
			if !dollar && strings.IndexByte(sql, '$') < 0 {
				continue
			}
			all, rules := commentDirective(sql, backslash, dollar)
			if first {
				ignoreAll, ignored, first = all, rules, false
				continue
			}
			ignoreAll, ignored = intersectDirectives(ignoreAll, ignored, all, rules)
		}
	}
	return ignoreAll, ignored
}

// commentDirective collects the directives in sql's comments under one
// literal reading. Comments are found with every marker any dialect accepts;
// a directive is then dropped if the strict markers (no #, MySQL's "-- " that
// needs trailing whitespace) put it inside a literal, as with Postgres'
// "a # b" XOR or MySQL's "1--'x'".
func commentDirective(sql string, backslash, dollar bool) (all bool, rules map[string]bool) {
	comments, _ := lexSQL(sql, backslash, dollar, false)
	_, literals := lexSQL(sql, backslash, dollar, true)
	for _, c := range comments {
		for _, m := range ignoreTokenRe.FindAllStringSubmatchIndex(sql[c.lo:c.hi], -1) {
			if inSpans(literals, c.lo+m[0]) {
				continue
			}
			list := ""
			if m[2] >= 0 {
				list = strings.TrimSpace(sql[c.lo+m[2] : c.lo+m[3]])
			}
			if list == "" {
				return true, nil
			}
			if rules == nil {
				rules = make(map[string]bool)
			}
			for name := range strings.SplitSeq(list, ",") {
				if name = strings.TrimSpace(name); name != "" {
					rules[name] = true
				}
			}
		}
	}
	return false, rules
}

// lexSQL returns the comment and literal spans of sql under one reading.
// strict limits comment markers to those every dialect agrees on.
func lexSQL(sql string, backslash, dollar, strict bool) (comments, literals []span) {
	for i := 0; i < len(sql); {
		c := sql[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			j := scanQuotedRun(sql, i, backslash && c != '`')
			literals = append(literals, span{i, j})
			i = j
		case c == '$' && dollar:
			j, _, ok := scanDollarQuoted(sql, i)
			if !ok {
				i++
				continue
			}
			literals = append(literals, span{i, j})
			i = j
		case isLineComment(sql, i, strict):
			j := skipLineComment(sql, i)
			comments = append(comments, span{i, j})
			i = j
		case c == '/' && i+1 < len(sql) && sql[i+1] == '*':
			j := min(skipBlockComment(sql, i), len(sql))
			comments = append(comments, span{i, j})
			i = j
		default:
			i++
		}
	}
	return comments, literals
}

// isLineComment reports whether a -- or # comment starts at sql[i]. Strictly,
// # is not a comment (Postgres XOR) and -- needs a following space or control
// byte (MySQL).
func isLineComment(sql string, i int, strict bool) bool {
	if sql[i] == '#' {
		return !strict
	}
	if sql[i] != '-' || i+1 >= len(sql) || sql[i+1] != '-' {
		return false
	}
	return !strict || i+2 >= len(sql) || sql[i+2] <= ' '
}

func inSpans(spans []span, pos int) bool {
	for _, s := range spans {
		if pos >= s.lo && pos < s.hi {
			return true
		}
	}
	return false
}

// intersectDirectives keeps what both readings suppress.
func intersectDirectives(aAll bool, a map[string]bool, bAll bool, b map[string]bool) (bool, map[string]bool) {
	switch {
	case aAll && bAll:
		return true, nil
	case aAll:
		return false, b
	case bAll:
		return false, a
	}
	var out map[string]bool
	for name := range a {
		if b[name] {
			if out == nil {
				out = make(map[string]bool)
			}
			out[name] = true
		}
	}
	return false, out
}

// ParseIgnoreComment parses the text of a single comment for a
// sqlguard:ignore directive. It is used by the static scanner to honor
// `// sqlguard:ignore` / `// sqlguard:ignore:rule-a,rule-b` annotations in Go
// source. found reports whether a directive was present; all is true for a
// bare directive (suppress every rule); rules holds the named rules
// otherwise.
func ParseIgnoreComment(text string) (all bool, rules map[string]bool, found bool) {
	m := ignoreTokenRe.FindStringSubmatch(text)
	if m == nil {
		return false, nil, false
	}
	list := strings.TrimSpace(m[1])
	if list == "" {
		return true, nil, true
	}
	rules = make(map[string]bool)
	for name := range strings.SplitSeq(list, ",") {
		if name = strings.TrimSpace(name); name != "" {
			rules[name] = true
		}
	}
	return false, rules, true
}

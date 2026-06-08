package analyzer

import (
	"regexp"
	"strings"
)

// ignoreDirectiveRe matches a sqlguard:ignore directive inside a SQL or Go
// comment. The leading comment marker (--, /*, #, //) anchors it so the
// token is honored only in comment context, not when the literal text
// happens to appear inside a string. An optional `:rule-a, rule-b` list
// scopes the suppression to specific rules; without it, all rules are
// suppressed for the statement.
var ignoreDirectiveRe = regexp.MustCompile(`(?i)(?:--|/\*|#|//)[^\n]*?sqlguard:ignore(?::\s*([a-z0-9_,\s-]+))?`)

// ignoreTokenRe matches the bare directive in text that is already known to
// be a comment (e.g. go/ast comment text with the marker stripped). No
// comment marker is required here because the whole string is comment
// context.
var ignoreTokenRe = regexp.MustCompile(`(?i)sqlguard:ignore(?::\s*([a-z0-9_,\s-]+))?`)

// parseIgnoreDirective scans raw SQL for `sqlguard:ignore` directives.
// It returns ignoreAll=true if any directive has no rule list, otherwise a
// set of rule names to suppress. The result is empty when no directive is
// present, so the common path allocates nothing.
func parseIgnoreDirective(sql string) (ignoreAll bool, ignored map[string]bool) {
	if !strings.Contains(strings.ToLower(sql), "sqlguard:ignore") {
		return false, nil
	}
	for _, m := range ignoreDirectiveRe.FindAllStringSubmatch(sql, -1) {
		list := strings.TrimSpace(m[1])
		if list == "" {
			return true, nil
		}
		if ignored == nil {
			ignored = make(map[string]bool)
		}
		for name := range strings.SplitSeq(list, ",") {
			if name = strings.TrimSpace(name); name != "" {
				ignored[name] = true
			}
		}
	}
	return false, ignored
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

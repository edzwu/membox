package docrewrite

import (
	"strings"
	"unicode"
)

// NormalizeCodeFences fixes PDF/LLM fence language tags so C/C++ samples are
// highlighted as cpp instead of txt/text/c/plain (common conversion debris).
func NormalizeCodeFences(md string) string {
	if !strings.Contains(md, "```") {
		return md
	}
	lines := strings.Split(md, "\n")
	var out []string
	for i := 0; i < len(lines); {
		line := lines[i]
		trim := strings.TrimSpace(line)
		if !strings.HasPrefix(trim, "```") {
			out = append(out, line)
			i++
			continue
		}
		info := strings.TrimSpace(strings.TrimPrefix(trim, "```"))
		// Collect fence body until closing ```.
		j := i + 1
		var body []string
		for j < len(lines) {
			if strings.TrimSpace(lines[j]) == "```" {
				break
			}
			body = append(body, lines[j])
			j++
		}
		lang := fenceLang(info)
		bodyText := strings.Join(body, "\n")
		if shouldTagCpp(lang, bodyText) {
			// Preserve indentation of the opening fence line.
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			out = append(out, indent+"```cpp")
		} else {
			out = append(out, line)
		}
		out = append(out, body...)
		if j < len(lines) {
			out = append(out, lines[j])
			i = j + 1
		} else {
			i = j
		}
	}
	return strings.Join(out, "\n")
}

func fenceLang(info string) string {
	info = strings.TrimSpace(info)
	if info == "" {
		return ""
	}
	// info may be "cpp {.line-numbers}" etc. — take first token.
	fields := strings.Fields(info)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(fields[0])
}

func shouldTagCpp(lang, body string) bool {
	switch lang {
	case "cpp":
		return false // already preferred tag
	case "c++", "cxx", "cc":
		return true // normalize aliases → cpp
	case "txt", "text", "plain", "plaintext", "output", "c", "":
		// retag weak/wrong labels when body looks like C family
		return looksLikeCFamily(body)
	default:
		return false
	}
}

func looksLikeCFamily(body string) bool {
	s := strings.TrimSpace(body)
	if s == "" {
		return false
	}
	// Strong C++ markers
	strong := []string{
		"#include", "namespace ", "template<", "template <", "std::",
		"nullptr", "constexpr", "->", "::", "char *", "char*",
		"void ", "int ", "size_t", "struct ", "return ",
		"worker_", "ds_", "SYSTEM_",
	}
	lower := s
	hits := 0
	for _, p := range strong {
		if strings.Contains(lower, p) {
			hits++
		}
	}
	// Semicolon-terminated lines are common in C/C++ samples from this series.
	semi := 0
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasSuffix(t, ";") || strings.HasSuffix(t, "{") || strings.HasSuffix(t, "}") {
			semi++
		}
	}
	if hits >= 2 {
		return true
	}
	if hits >= 1 && semi >= 2 {
		return true
	}
	// Bare braces + identifiers (truncated PDF snippets)
	if semi >= 3 && hasCIdent(s) {
		return true
	}
	return false
}

func hasCIdent(s string) bool {
	// crude: letter/underscore run containing '_' (common in this codebase style)
	run := 0
	hasUnderscore := false
	for _, r := range s {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			if r == '_' {
				hasUnderscore = true
			}
			run++
			if run >= 8 && hasUnderscore {
				return true
			}
			continue
		}
		run = 0
		hasUnderscore = false
	}
	return false
}

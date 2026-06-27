package tui

import "strings"

func completionCandidates() []string {
	return []string{
		"note ls",
		"note search ",
		"workspace add ",
		"workspace rm ",
		"workspace ls",
		"q",
		"help",
	}
}

func progressiveCompletions(input string, notes []noteItem) []string {
	parts := strings.Fields(input)
	trailingSpace := strings.HasSuffix(input, " ")

	roots := []string{"note", "workspace", "q", "help"}
	noteSubs := []string{"ls", "search"}
	workspaceSubs := []string{"add", "rm", "ls"}

	if len(parts) == 0 {
		return roots
	}

	if len(parts) == 1 && !trailingSpace {
		return filterPrefix(roots, parts[0])
	}

	switch parts[0] {
	case "note":
		if len(parts) == 1 && trailingSpace {
			return prefixAll("note ", noteSubs)
		}
		if len(parts) == 2 && !trailingSpace {
			return prefixAll("note ", filterPrefix(noteSubs, parts[1]))
		}
		if len(parts) == 2 && trailingSpace && parts[1] == "search" {
			return []string{"note search "}
		}

	case "workspace", "ws":
		if len(parts) == 1 && trailingSpace {
			return prefixAll("workspace ", workspaceSubs)
		}
		if len(parts) == 2 && !trailingSpace {
			return prefixAll("workspace ", filterPrefix(workspaceSubs, parts[1]))
		}
		if len(parts) == 2 && trailingSpace && (parts[1] == "add" || parts[1] == "rm") {
			return []string{"workspace " + parts[1] + " "}
		}
	}

	return nil
}

func filterPrefix(items []string, prefix string) []string {
	var out []string
	for _, item := range items {
		if strings.HasPrefix(item, prefix) && item != prefix {
			out = append(out, item)
		}
	}
	return out
}

func prefixAll(prefix string, items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, prefix+item)
	}
	return out
}

func shortID(uuid string) string {
	if len(uuid) <= 5 {
		return uuid
	}
	return uuid[:5]
}

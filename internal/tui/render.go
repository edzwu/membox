package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

var (
	listLineRegex = regexp.MustCompile(`^(\S+)\s+(\S+)\s+(.+)$`)
	allLineRegex  = regexp.MustCompile(`^(\S+)\s+(\S+)\s+(\S+)\s+(.+)$`)
)

func renderOutput(raw string, width int) (string, error) {
	raw = strings.TrimRight(raw, "\n")
	if raw == "" {
		return "(no output)", nil
	}

	if isNoteList(raw) {
		return renderTable(raw, width), nil
	}

	// Plain text is returned as-is to avoid glamour emitting truecolor/OSC
	// escape sequences that confuse terminals running inside an alt screen.
	return raw, nil
}

func isNoteList(s string) bool {
	lines := strings.Split(s, "\n")
	if len(lines) == 0 {
		return false
	}
	header := lines[0]
	return strings.HasPrefix(header, "UUID") && strings.Contains(header, "Updated") && strings.Contains(header, "Title")
}

func renderTable(raw string, width int) string {
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 {
		return ""
	}

	header := lines[0]
	fields := strings.Fields(header)
	isAll := len(fields) >= 4 && fields[1] == "Created"

	gap := "    "

	var uuidW, createdW, updatedW, titleW int
	if isAll {
		uuidW = max(len("UUID"), 5)
		createdW = max(len("Created"), 10)
		updatedW = max(len("Updated"), 19)
		titleW = width - uuidW - createdW - updatedW - len(gap)*3
	} else {
		uuidW = max(len("UUID"), 5)
		updatedW = max(len("Updated"), 19)
		titleW = width - uuidW - updatedW - len(gap)*2
	}
	if titleW < 10 {
		titleW = 10
	}

	bold := lipgloss.NewStyle().Bold(true)
	var sb strings.Builder

	if isAll {
		sb.WriteString(formatRow(gap,
			bold.Render(padRight("UUID", uuidW)),
			bold.Render(padRight("Created", createdW)),
			bold.Render(padRight("Updated", updatedW)),
			bold.Render(padRight("Title", titleW))))
		for _, line := range lines[1:] {
			if line == "" {
				continue
			}
			m := allLineRegex.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			sb.WriteString("\n")
			sb.WriteString(formatRow(gap,
				padRight(m[1], uuidW),
				padRight(m[2], createdW),
				padRight(m[3], updatedW),
				truncate(m[4], titleW)))
		}
	} else {
		sb.WriteString(formatRow(gap,
			bold.Render(padRight("UUID", uuidW)),
			bold.Render(padRight("Updated", updatedW)),
			bold.Render(padRight("Title", titleW))))
		for _, line := range lines[1:] {
			if line == "" {
				continue
			}
			m := listLineRegex.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			sb.WriteString("\n")
			sb.WriteString(formatRow(gap,
				padRight(m[1], uuidW),
				padRight(m[2], updatedW),
				truncate(m[3], titleW)))
		}
	}
	return sb.String()
}

func formatRow(gap string, cols ...string) string {
	return strings.Join(cols, gap)
}

func padRight(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

func truncate(s string, w int) string {
	if len(s) <= w {
		return padRight(s, w)
	}
	if w <= 1 {
		return s[:w]
	}
	return s[:w-1] + "…"
}

func glamourRender(md string, width uint) (string, error) {
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(int(width)),
	)
	if err != nil {
		return "", err
	}
	return r.Render(md)
}

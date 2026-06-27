package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/earendil-works/membox/internal/app"
)

// outputMsg carries rendered command output to the model.
type outputMsg string

// errorMsg carries an error message to the model.
type errorMsg string

// notesMsg carries the list of notes loaded at startup.
type notesMsg []noteItem

// loadInitialNotes returns a command that lists the most recent notes.
func loadInitialNotes(a *app.App) tea.Cmd {
	return func() tea.Msg {
		notes, err := a.ListNotes(context.Background(), nil, nil, 50)
		if err != nil {
			return errorMsg(fmt.Sprintf("load notes: %v", err))
		}

		items := make([]noteItem, 0, len(notes))
		for _, n := range notes {
			items = append(items, noteItem{UUID: n.UUID, Title: n.Title})
		}
		return notesMsg(items)
	}
}

// runCommand executes a parsed TUI command through the app layer.
func runCommand(a *app.App, cmd parsedCommand) tea.Cmd {
	return func() tea.Msg {
		switch cmd.kind {
		case cmdListNotes:
			return listNotes(a)
		case cmdSearchNotes:
			return searchNotes(a, cmd.args[0])
		case cmdAddWorkspace:
			return addWorkspace(a, cmd.args[0])
		case cmdRemoveWorkspace:
			return removeWorkspace(a, cmd.args[0])
		case cmdListWorkspaces:
			return listWorkspaces(a)
		default:
			return errorMsg(fmt.Sprintf("unhandled command: %v", cmd.kind))
		}
	}
}

func listNotes(a *app.App) tea.Msg {
	notes, err := a.ListNotes(context.Background(), nil, nil, 20)
	if err != nil {
		return errorMsg(fmt.Sprintf("list notes: %v", err))
	}
	if len(notes) == 0 {
		return outputMsg("No notes found.")
	}

	var b strings.Builder
	fmt.Fprintln(&b, "UUID     Updated            Title")
	for _, n := range notes {
		fmt.Fprintf(&b, "%s  %s  %s\n", shortID(n.UUID), n.UpdatedAt.Format("2006-01-02-15:04:05"), n.Title)
	}
	return outputMsg(strings.TrimRight(b.String(), "\n"))
}

func searchNotes(a *app.App, query string) tea.Msg {
	results, err := a.SearchNotes(context.Background(), query, 20)
	if err != nil {
		return errorMsg(fmt.Sprintf("search notes: %v", err))
	}
	if len(results) == 0 {
		return outputMsg("No results.")
	}

	var b strings.Builder
	for _, r := range results {
		fmt.Fprintf(&b, "%s  %s\n", r.Note.UUID, r.Note.Title)
		if r.Snippet != "" {
			fmt.Fprintf(&b, "    %s\n", r.Snippet)
		}
	}
	return outputMsg(strings.TrimRight(b.String(), "\n"))
}

func addWorkspace(a *app.App, dir string) tea.Msg {
	if err := a.AddWorkspace(dir); err != nil {
		return errorMsg(fmt.Sprintf("add workspace: %v", err))
	}
	if _, err := a.Sync(context.Background()); err != nil {
		return errorMsg(fmt.Sprintf("sync after add: %v", err))
	}
	return outputMsg(fmt.Sprintf("Added workspace directory: %s", dir))
}

func removeWorkspace(a *app.App, dir string) tea.Msg {
	if err := a.RemoveWorkspace(dir); err != nil {
		return errorMsg(fmt.Sprintf("remove workspace: %v", err))
	}
	if _, err := a.Sync(context.Background()); err != nil {
		return errorMsg(fmt.Sprintf("sync after remove: %v", err))
	}
	return outputMsg(fmt.Sprintf("Removed workspace directory: %s", dir))
}

func listWorkspaces(a *app.App) tea.Msg {
	ws := a.Workspaces()
	if len(ws.Notes) == 0 {
		return outputMsg("No workspace directories configured.")
	}

	var b strings.Builder
	fmt.Fprintln(&b, "Workspace directories:")
	for _, dir := range ws.Notes {
		fmt.Fprintf(&b, "  %s\n", dir)
	}
	return outputMsg(strings.TrimRight(b.String(), "\n"))
}

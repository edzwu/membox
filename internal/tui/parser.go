package tui

import (
	"errors"
	"fmt"
	"strings"
)

// commandKind identifies the high-level action requested by the user.
type commandKind int

const (
	cmdUnknown commandKind = iota
	cmdQuit
	cmdHelp
	cmdListNotes
	cmdSearchNotes
	cmdAddWorkspace
	cmdListWorkspaces
	cmdRemoveWorkspace
)

// parsedCommand is the result of parsing a TUI input line.
type parsedCommand struct {
	kind commandKind
	args []string
	help string // set when kind == cmdUnknown to suggest usage
}

// parseCommand interprets a raw input line into a structured command.
func parseCommand(input string) (parsedCommand, error) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return parsedCommand{}, nil
	}

	switch parts[0] {
	case "q", "quit", "exit":
		return parsedCommand{kind: cmdQuit}, nil
	case "?", "h", "help":
		return parsedCommand{kind: cmdHelp}, nil
	case "note":
		return parseNoteCommand(parts[1:])
	case "workspace", "ws":
		return parseWorkspaceCommand(parts[1:])
	}

	return parsedCommand{kind: cmdUnknown}, nil
}

func parseNoteCommand(args []string) (parsedCommand, error) {
	if len(args) == 0 {
		return parsedCommand{kind: cmdUnknown, help: "usage: note ls | note search <query>"}, nil
	}

	switch args[0] {
	case "ls", "list":
		return parsedCommand{kind: cmdListNotes}, nil
	case "search":
		if len(args) < 2 {
			return parsedCommand{kind: cmdUnknown, help: "usage: note search <query>"}, nil
		}
		return parsedCommand{kind: cmdSearchNotes, args: []string{strings.Join(args[1:], " ")}}, nil
	}

	return parsedCommand{kind: cmdUnknown, help: fmt.Sprintf("unknown note subcommand: %q", args[0])}, nil
}

func parseWorkspaceCommand(args []string) (parsedCommand, error) {
	if len(args) == 0 {
		return parsedCommand{kind: cmdListWorkspaces}, nil
	}

	switch args[0] {
	case "add":
		if len(args) < 2 {
			return parsedCommand{kind: cmdUnknown, help: "usage: workspace add <path>"}, nil
		}
		return parsedCommand{kind: cmdAddWorkspace, args: []string{strings.Join(args[1:], " ")}}, nil
	case "rm", "remove":
		if len(args) < 2 {
			return parsedCommand{kind: cmdUnknown, help: "usage: workspace rm <path>"}, nil
		}
		return parsedCommand{kind: cmdRemoveWorkspace, args: []string{strings.Join(args[1:], " ")}}, nil
	case "ls", "list":
		return parsedCommand{kind: cmdListWorkspaces}, nil
	}

	return parsedCommand{kind: cmdUnknown, help: fmt.Sprintf("unknown workspace subcommand: %q", args[0])}, nil
}

// errNoop indicates the input line parsed to an empty command.
var errNoop = errors.New("empty command")

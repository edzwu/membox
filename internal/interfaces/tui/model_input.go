package tui

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"membox"
	"membox/internal/interfaces/host"
)

func (m Model) updateFilterInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+p" {
		return m, m.cycleMediaScope()
	}
	if m.inputMode == inputModeCmd {
		return m.updateCommandInput(msg)
	}
	if m.inputMode == inputModeAgent {
		return m.updateAgentInput(msg)
	}
	// ctrl+o while typing a filter opens the match/case options panel.
	if msg.String() == "ctrl+o" {
		m.filterOptionsVisible = !m.filterOptionsVisible
		if m.filterOptionsVisible {
			m.filterOptionsSelected = 0
		}
		return m, nil
	}
	if m.filterOptionsVisible {
		return m.updateFilterOptions(msg)
	}
	// Ctrl+C deletes a character in the input box (it no longer quits; Ctrl+D quits).
	if msg.String() == "ctrl+c" {
		msg = tea.KeyMsg{Type: tea.KeyBackspace}
	}
	switch msg.String() {
	case "esc":
		m.filterErr = nil
		m.hideInput()
		return m, m.filterChanged(nil)
	case "tab":
		// Tab is free in filter mode (pinning moved back to Enter).
		return m, nil
	case "ctrl+f":
		// Scope toggle (name ⇄ full content). Also available under ctrl+o → scope.
		m.toggleSearchScope()
		return m, m.filterChanged(nil)
	case "ctrl+g":
		m.detailsVisible = !m.detailsVisible
		m.keepSelectionVisible()
		return m, nil
	case "enter":
		// Enter with a draft pins it as a filter tag and returns to the tree
		// without opening a document. Empty draft opens the selection (fzf).
		handled, err := m.commitFilterToken()
		if handled {
			m.filterErr = err
			if err != nil {
				return m, nil
			}
			m.hideInput()
			return m, m.filterChanged(nil)
		}
		if m.commitTextFilter() {
			m.hideInput()
			return m, m.filterChanged(nil)
		}
		m.rememberFilterHistory(m.input.Value())
		m.hideInput()
		if document, ok := m.selectedDocument(); ok {
			m.loading = true
			if document.MediaType == "application/pdf" {
				return m, tea.Batch(m.spinner.Tick, openCmd(m.ctx, m.app, m.launcher, document.ID))
			}
			if m.viewerMode == "web" {
				return m, tea.Batch(m.spinner.Tick, openDocumentWebCmd(m.ctx, m.app, m.launcher, document.ID))
			}
			return m, tea.Batch(m.spinner.Tick, resolveViewerCmd(m.ctx, m.app, document.ID))
		}
		return m, nil
	case "up", "down":
		// While the filter is focused, ↑/↓ recall prior filter queries — not
		// the tree. Esc first to move the selection again.
		return m.navigateFilterHistory(msg.String())
	case "pgup", "pgdown", "home", "end":
		// Tree/list paging stays locked until the filter input blurs.
		return m, nil
	case "ctrl+u":
		m.input.SetValue("")
		m.historyIndex = len(m.filterHistory)
		return m, m.filterChanged(nil)
	case "backspace":
		if m.input.Value() == "" && m.removeLastFilter() {
			return m, m.filterChanged(nil)
		}
	case "space", " ":
		handled, err := m.commitFilterToken()
		if handled {
			m.filterErr = err
			if err != nil {
				return m, nil
			}
			return m, m.filterChanged(nil)
		}
	}

	m.spaceSequence = 0
	m.lastKeyAt = time.Time{}
	m.filterErr = nil
	m.statusMessage = ""
	var command tea.Cmd
	m.input, command = m.input.Update(msg)
	return m, m.filterChanged(command)
}

// navigateFilterHistory walks filterHistory like a shell prompt. Recalling a
// prior query also re-runs the live filter so the tree matches the draft.
func (m Model) navigateFilterHistory(key string) (tea.Model, tea.Cmd) {
	if len(m.filterHistory) == 0 {
		return m, nil
	}
	if key == "up" {
		if m.historyIndex > 0 {
			m.historyIndex--
		}
	} else if m.historyIndex < len(m.filterHistory) {
		m.historyIndex++
	}
	if m.historyIndex == len(m.filterHistory) {
		m.input.SetValue("")
	} else {
		m.input.SetValue(m.filterHistory[m.historyIndex])
	}
	m.input.CursorEnd()
	return m, m.filterChanged(nil)
}

func (m Model) updateAgentInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.hideInput()
		return m, nil
	case "enter":
		if strings.TrimSpace(m.input.Value()) == "" {
			return m, nil
		}
		// Attach current document as optional turn context.
		if doc, ok := m.selectedDocument(); ok {
			m.agent.documentID = doc.ID
			m.agent.documentTitle = displayTitle(doc.Title, doc.Path)
		} else {
			m.agent.documentID = ""
			m.agent.documentTitle = ""
		}
		return m.submitAgentPrompt()
	case "ctrl+c":
		if m.agent.runID != "" {
			return m.abortAgentRun()
		}
		msg = tea.KeyMsg{Type: tea.KeyBackspace}
		var command tea.Cmd
		m.input, command = m.input.Update(msg)
		return m, command
	case "ctrl+u":
		m.input.SetValue("")
		return m, nil
	default:
		m.statusMessage = ""
		var command tea.Cmd
		m.input, command = m.input.Update(msg)
		return m, command
	}
}

func (m Model) updateCommandInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.cmdMenuVisible {
			m.cmdMenuVisible = false
			return m, nil
		}
		m.filterErr = nil
		m.hideInput()
		return m, nil
	case "up", "down":
		if m.cmdMenuVisible {
			if len(m.cmdSuggestions) == 0 {
				return m, nil
			}
			if msg.String() == "up" {
				m.cmdSelected = max(0, m.cmdSelected-1)
			} else {
				m.cmdSelected = min(len(m.cmdSuggestions)-1, m.cmdSelected+1)
			}
			return m, nil
		}
		if len(m.commandHistory) == 0 {
			return m, nil
		}
		if msg.String() == "up" {
			if m.historyIndex > 0 {
				m.historyIndex--
			}
		} else {
			if m.historyIndex < len(m.commandHistory) {
				m.historyIndex++
			}
		}
		if m.historyIndex == len(m.commandHistory) {
			m.input.SetValue("")
		} else {
			m.input.SetValue(m.commandHistory[m.historyIndex])
		}
		m.input.CursorEnd()
		return m, nil
	case "tab":
		m.cmdSuggestions = m.commandSuggestions()
		m.cmdSelected = 0
		m.cmdMenuVisible = len(m.cmdSuggestions) > 0
		if len(m.cmdSuggestions) == 0 {
			m.filterErr = fmt.Errorf("no command suggestions")
		} else {
			m.filterErr = nil
		}
		return m, nil
	case "enter":
		if m.cmdMenuVisible && len(m.cmdSuggestions) > 0 {
			return m.chooseCommandSuggestion(m.cmdSuggestions[m.cmdSelected])
		}
		return m.executeCommandInput()
	case "ctrl+u":
		m.input.SetValue("")
		m.historyIndex = len(m.commandHistory)
		m.cmdMenuVisible = false
		return m, nil
	default:
		m.cmdMenuVisible = false
		m.filterErr = nil
		m.statusMessage = ""
		var command tea.Cmd
		m.input, command = m.input.Update(msg)
		return m, command
	}
}

func (m Model) chooseCommandSuggestion(suggestion commandSuggestion) (tea.Model, tea.Cmd) {
	if suggestion.Action != nil {
		m.loading = true
		m.cmdMenuVisible = false
		return m, tea.Batch(m.spinner.Tick, func() tea.Msg { return suggestion.Action() })
	}
	m.replaceLastCommandToken(suggestion.Value)
	m.cmdMenuVisible = false
	m.cmdSuggestions = nil
	m.filterErr = nil
	return m, nil
}

func (m *Model) replaceLastCommandToken(value string) {
	raw := m.input.Value()
	if strings.HasSuffix(raw, " ") || strings.HasSuffix(raw, "\t") {
		m.input.SetValue(raw + value)
	} else {
		trimmed := strings.TrimRight(raw, " \t")
		separator := strings.LastIndexAny(trimmed, " \t")
		if separator < 0 {
			m.input.SetValue(value)
		} else {
			m.input.SetValue(trimmed[:separator+1] + value)
		}
	}
	if !strings.HasSuffix(m.input.Value(), " ") {
		m.input.SetValue(m.input.Value() + " ")
	}
	m.input.CursorEnd()
}

func (m Model) executeCommandInput() (tea.Model, tea.Cmd) {
	tokens := commandTokens(m.input.Value())
	if len(tokens) == 0 {
		return m, nil
	}
	action, usage, err := m.commandAction(tokens)
	if err != nil {
		m.filterErr = fmt.Errorf("%v; usage: %s", err, usage)
		return m, nil
	}
	m.loading = true
	m.cmdMenuVisible = false
	if len(tokens) >= 2 && tokens[0] == "pdf" && tokens[1] == "convert" {
		target := ""
		if document, ok := m.selectedDocument(); ok {
			target = document.ID
		}
		if len(tokens) == 3 && tokens[2] != "@selected" {
			target = tokens[2]
		}
		progressTick := m.beginPDFProgress(target)
		return m, tea.Batch(m.spinner.Tick, startPDFConversionCmd(m.ctx, m.app, target, m.pdfProgressSequence), progressTick)
	}
	return m, tea.Batch(m.spinner.Tick, func() tea.Msg { return action() })
}

func commandTokens(value string) []string {
	return strings.Fields(strings.TrimSpace(value))
}

func (m Model) commandInputStructure() (tokens []string, index int, partial string) {
	raw := m.input.Value()
	tokens = commandTokens(raw)
	if strings.HasSuffix(raw, " ") || strings.HasSuffix(raw, "\t") {
		return tokens, len(tokens), ""
	}
	if len(tokens) == 0 {
		return nil, 0, ""
	}
	return tokens[:len(tokens)-1], len(tokens) - 1, tokens[len(tokens)-1]
}

func (m Model) commandSuggestions() []commandSuggestion {
	tokens, index, partial := m.commandInputStructure()
	if index == 0 {
		return filterCommandSuggestions([]commandSuggestion{
			{Value: "doc", Display: "doc", Description: "Create and manage documents"},
			{Value: "topic", Display: "topic", Description: "Manage topic documents"},
			{Value: "link", Display: "link", Description: "Manage document links"},
			{Value: "pdf", Display: "pdf", Description: "Convert selected PDF to Markdown"},
			{Value: "rename", Display: "rename", Description: "Rename file or virtual PDF title (keeps UUID)"},
			{Value: "web", Display: "web", Description: "Control the Web Companion"},
		}, partial)
	}
	if index == 1 {
		switch tokens[0] {
		case "doc", "note":
			return filterCommandSuggestions([]commandSuggestion{
				{Value: "new", Display: "new", Description: "Create a document from the selected document"},
				{Value: "view", Display: "view", Description: "Open a document with the configured viewer"},
			}, partial)
		case "topic":
			return filterCommandSuggestions([]commandSuggestion{
				{Value: "create", Display: "create", Description: "Create a topic document"},
				{Value: "list", Display: "list", Description: "List topics"},
				{Value: "add", Display: "add", Description: "Add document to topic"},
				{Value: "remove", Display: "remove", Description: "Remove document from topic"},
				{Value: "documents", Display: "documents", Description: "List documents in topic"},
			}, partial)
		case "link":
			return filterCommandSuggestions([]commandSuggestion{
				{Value: "add", Display: "add", Description: "Link two documents"},
				{Value: "remove", Display: "remove", Description: "Remove a document link"},
				{Value: "list", Display: "list", Description: "Show links and topics"},
			}, partial)
		case "pdf":
			return filterCommandSuggestions([]commandSuggestion{
				{Value: "convert", Display: "convert", Description: "Upload selected PDF and publish Markdown"},
				{Value: "server", Display: "server", Description: "Set converter URL: pdf server http://host:8000"},
			}, partial)
		case "web":
			return filterCommandSuggestions([]commandSuggestion{
				{Value: "status", Display: "status", Description: "Show Web Companion state"},
				{Value: "open", Display: "open", Description: "Open the reader in the browser"},
				{Value: "start", Display: "start", Description: "Start the Web Companion"},
				{Value: "stop", Display: "stop", Description: "Stop the Web Companion"},
				{Value: "keep", Display: "keep", Description: "Keep web running after TUI exit"},
			}, partial)
		}
		return nil
	}
	return m.commandArgumentSuggestions(tokens, index, partial)
}

func filterCommandSuggestions(suggestions []commandSuggestion, partial string) []commandSuggestion {
	if partial == "" {
		return suggestions
	}
	partial = strings.ToLower(partial)
	out := make([]commandSuggestion, 0, len(suggestions))
	for _, suggestion := range suggestions {
		value := strings.ToLower(suggestion.Value)
		display := strings.ToLower(suggestion.Display)
		description := strings.ToLower(suggestion.Description)
		if strings.HasPrefix(value, partial) || strings.HasPrefix(display, partial) || strings.Contains(description, partial) {
			out = append(out, suggestion)
		}
	}
	return out
}

func (m Model) commandArgumentSuggestions(tokens []string, index int, partial string) []commandSuggestion {
	if len(tokens) < 2 {
		return nil
	}
	resource, verb := tokens[0], tokens[1]
	selected := ""
	if document, ok := m.selectedDocument(); ok {
		selected = document.ID
	}
	documentSuggestions := func() []commandSuggestion {
		var out []commandSuggestion
		if selected != "" {
			out = append(out, commandSuggestion{Value: "@selected", Display: "@selected", Description: "Current document: " + selected})
		}
		for _, candidate := range m.items {
			label := displayTitle(candidate.document.Title, candidate.document.Path)
			out = append(out, commandSuggestion{Value: candidate.document.ID, Display: shortID(candidate.document.ID), Description: candidate.filename + " " + label})
		}
		return filterCommandSuggestions(out, partial)
	}
	pdfSuggestions := func() []commandSuggestion {
		var out []commandSuggestion
		if document, ok := m.selectedDocument(); ok && document.MediaType == "application/pdf" {
			out = append(out, commandSuggestion{Value: "@selected", Display: "@selected", Description: "Current PDF: " + selected})
		}
		for _, candidate := range m.items {
			if candidate.document.MediaType != "application/pdf" {
				continue
			}
			out = append(out, commandSuggestion{Value: candidate.document.ID, Display: shortID(candidate.document.ID), Description: treeItemLabel(candidate)})
		}
		return filterCommandSuggestions(out, partial)
	}
	topicSuggestions := func() []commandSuggestion {
		var out []commandSuggestion
		for _, candidate := range m.items {
			base := filepath.Base(candidate.document.RelativePath)
			if !strings.HasPrefix(base, "topic-") || !strings.HasSuffix(strings.ToLower(base), ".md") {
				continue
			}
			name := strings.TrimSuffix(strings.TrimPrefix(base, "topic-"), filepath.Ext(base))
			out = append(out, commandSuggestion{Value: candidate.document.ID, Display: shortID(candidate.document.ID), Description: strings.ReplaceAll(name, "-", " ")})
		}
		return filterCommandSuggestions(out, partial)
	}
	switch resource {
	case "doc", "note":
		if verb == "view" && index == 2 {
			return documentSuggestions()
		}
	case "topic":
		switch verb {
		case "add", "remove":
			if index == 2 {
				return topicSuggestions()
			}
			if index == 3 {
				return documentSuggestions()
			}
		case "documents":
			if index == 2 {
				return topicSuggestions()
			}
		}
	case "link":
		if (verb == "add" || verb == "remove") && (index == 2 || index == 3) {
			return documentSuggestions()
		}
		if verb == "list" && index == 2 {
			return documentSuggestions()
		}
	case "pdf":
		if verb == "convert" && index == 2 {
			return pdfSuggestions()
		}
	}
	return nil
}

func (m Model) commandAction(tokens []string) (func() tea.Msg, string, error) {
	selected := ""
	var selectedDocument membox.DocumentView
	if document, ok := m.selectedDocument(); ok {
		selected = document.ID
		selectedDocument = document
	}
	selector := func(value string) string {
		if value == "@selected" {
			return selected
		}
		return value
	}
	if len(tokens) < 2 {
		return nil, strings.Join(tokens, " "), fmt.Errorf("command verb is required")
	}
	switch tokens[0] {
	case "doc", "note":
		if tokens[1] == "new" && len(tokens) >= 3 {
			title := strings.Join(tokens[2:], " ")
			return func() tea.Msg {
				result, err := m.app.CreateNote(m.ctx, membox.CreateNoteCommand{Title: title, FromSelector: selected})
				if err != nil {
					return commandResultMsg{err: err}
				}
				editor, editorErr := m.launcher.EditorCommand(m.ctx, result.Document.Path)
				if editorErr != nil {
					return noteCreatedMsg{document: result.Document, err: editorErr}
				}
				return noteCreatedMsg{document: result.Document, command: editor}
			}, "doc new <title>", nil
		}
		if tokens[1] == "view" && len(tokens) >= 3 {
			target := selector(tokens[2])
			webFlag := len(tokens) == 4 && tokens[3] == "--web"
			if len(tokens) > 3 && !webFlag {
				return nil, "doc view <document-id> [--web]", fmt.Errorf("invalid doc view arguments")
			}
			if webFlag {
				return func() tea.Msg {
					url, err := m.app.OpenDocumentWeb(m.ctx, target)
					if err != nil {
						return commandResultMsg{err: err}
					}
					command, openErr := m.launcher.OpenCommand(m.ctx, url)
					if openErr == nil {
						openErr = command.Run()
					}
					return openWebMsg{url: url, err: openErr}
				}, "doc view <document-id> --web", nil
			}
			return func() tea.Msg {
				mode, err := m.app.GetViewer(m.ctx)
				if err != nil {
					return commandResultMsg{err: err}
				}
				if mode == "web" {
					url, err := m.app.OpenDocumentWeb(m.ctx, target)
					if err != nil {
						return commandResultMsg{err: err}
					}
					command, openErr := m.launcher.OpenCommand(m.ctx, url)
					if openErr == nil {
						openErr = command.Run()
					}
					return openWebMsg{url: url, err: openErr}
				}
				// Leaf viewer: same path as pressing Enter — editReadyMsg runs the
				// viewer through tea.ExecProcess (TUI suspends, leaf gets the real
				// terminal, TUI resumes after exit).
				return resolveViewerCmd(m.ctx, m.app, target)()
			}, "doc view <document-id> [--web]", nil
		}
		return nil, "doc new <title> | doc view <document-id> [--web]", fmt.Errorf("invalid doc command")
	case "topic":
		switch tokens[1] {
		case "create":
			if len(tokens) < 3 {
				return nil, "topic create <name>", fmt.Errorf("topic name is required")
			}
			name := strings.Join(tokens[2:], " ")
			return func() tea.Msg {
				result, err := m.app.CreateTopic(m.ctx, membox.CreateTopicCommand{Name: name})
				if err != nil {
					return commandResultMsg{err: err}
				}
				return topicCreatedMsg{topic: result.Topic}
			}, "topic create <name>", nil
		case "list":
			if len(tokens) != 2 {
				return nil, "topic list", fmt.Errorf("topic list accepts no arguments")
			}
			return func() tea.Msg {
				topics, err := m.app.ListTopics(m.ctx, membox.ListTopicsQuery{})
				if err != nil {
					return commandResultMsg{err: err}
				}
				return commandResultMsg{text: fmt.Sprintf("%d topic(s): %s", len(topics), topicNames(topics))}
			}, "topic list", nil
		case "add", "remove":
			if len(tokens) != 4 {
				return nil, "topic " + tokens[1] + " <topic-id> <document-id>", fmt.Errorf("topic and document are required")
			}
			command := membox.TopicMembershipCommand{DocumentSelector: selector(tokens[3]), TopicSelector: selector(tokens[2])}
			return func() tea.Msg {
				var result membox.TopicMembershipResult
				var err error
				if tokens[1] == "add" {
					result, err = m.app.AddDocumentTopic(m.ctx, command)
				} else {
					result, err = m.app.RemoveDocumentTopic(m.ctx, command)
				}
				if err != nil {
					return commandResultMsg{err: err}
				}
				verb := "added to"
				if tokens[1] == "remove" {
					verb = "removed from"
				}
				return commandResultMsg{text: fmt.Sprintf("Document %s topic %q", verb, result.Topic.Name)}
			}, "topic " + tokens[1] + " <topic-id> <document-id>", nil
		case "documents":
			if len(tokens) != 3 {
				return nil, "topic documents <topic-id>", fmt.Errorf("topic is required")
			}
			return func() tea.Msg {
				result, err := m.app.ListTopicDocuments(m.ctx, membox.ListTopicDocumentsQuery{Selector: selector(tokens[2])})
				if err != nil {
					return commandResultMsg{err: err}
				}
				return commandResultMsg{text: fmt.Sprintf("topic %q has %d document(s)", result.Topic.Name, len(result.Documents))}
			}, "topic documents <topic-id>", nil
		}
	case "link":
		switch tokens[1] {
		case "add", "remove":
			if len(tokens) != 4 {
				return nil, "link " + tokens[1] + " <from-id> <to-id>", fmt.Errorf("from and to documents are required")
			}
			fromSelector, toSelector := selector(tokens[2]), selector(tokens[3])
			return func() tea.Msg {
				var err error
				if tokens[1] == "add" {
					_, err = m.app.LinkDocuments(m.ctx, membox.LinkDocumentsCommand{FromSelector: fromSelector, ToSelector: toSelector})
				} else {
					_, err = m.app.UnlinkDocuments(m.ctx, membox.UnlinkDocumentsCommand{FromSelector: fromSelector, ToSelector: toSelector})
				}
				if err != nil {
					return commandResultMsg{err: err}
				}
				verb := "linked"
				if tokens[1] == "remove" {
					verb = "unlinked"
				}
				return commandResultMsg{text: fmt.Sprintf("Documents %s", verb)}
			}, "link " + tokens[1] + " <from-id> <to-id>", nil
		case "list":
			if len(tokens) != 3 {
				return nil, "link list <document-id>", fmt.Errorf("document is required")
			}
			return graphFocusCmd(m.ctx, m.app, selector(tokens[2]), "thread"), "link list <document-id>", nil
		case "graph":
			if len(tokens) != 3 {
				return nil, "link graph <document-id>", fmt.Errorf("document is required")
			}
			return graphFocusCmd(m.ctx, m.app, selector(tokens[2]), "star"), "link graph <document-id>", nil
		}
	case "pdf":
		switch tokens[1] {
		case "convert":
			if m.pdfConversionActive {
				return nil, "pdf convert [document-id]", fmt.Errorf("a PDF conversion is already running")
			}
			target := selected
			if len(tokens) == 3 {
				target = selector(tokens[2])
			} else if len(tokens) != 2 {
				return nil, "pdf convert [document-id]", fmt.Errorf("expected zero or one document")
			}
			if target == "" {
				return nil, "pdf convert [document-id]", fmt.Errorf("no PDF is selected")
			}
			return func() tea.Msg {
				result, err := m.app.ConvertPDF(m.ctx, membox.ConvertPDFCommand{Selector: target})
				return pdfConvertedMsg{result: result, err: err}
			}, "pdf convert [document-id]", nil
		case "server":
			if len(tokens) != 3 {
				return nil, "pdf server <url>", fmt.Errorf("converter server URL is required")
			}
			serverURL := tokens[2]
			return func() tea.Msg {
				config, err := m.app.SetPDFConverterServer(m.ctx, serverURL)
				if err != nil {
					return commandResultMsg{err: err}
				}
				return commandResultMsg{text: "PDF converter: " + config.ServerURL}
			}, "pdf server <url>", nil
		default:
			return nil, "pdf convert [document-id] | pdf server <url>", fmt.Errorf("invalid pdf command")
		}
	case "rename":
		// Rename operates on the highlighted document. Markdown remains
		// filesystem-authoritative; for PDF this is a virtual title stored in
		// catalog metadata and the managed file path is left untouched.
		if len(tokens) < 2 {
			return nil, "rename <new-name>", fmt.Errorf("new name is required")
		}
		if selected == "" {
			return nil, "rename <new-name>", fmt.Errorf("no document is selected")
		}
		name := strings.Join(tokens[1:], " ")
		if selectedDocument.MediaType == "application/pdf" {
			return func() tea.Msg {
				document, err := m.app.UpdatePDFMetadata(m.ctx, membox.UpdatePDFMetadataCommand{Selector: selected, Title: &name})
				if err != nil {
					return renamedMsg{virtual: true, err: err}
				}
				return renamedMsg{documentID: document.ID, path: document.Path, title: document.Title, virtual: true}
			}, "rename <new-title>", nil
		}
		return func() tea.Msg {
			result, err := m.app.RenameDocument(m.ctx, membox.RenameDocumentCommand{Selector: selected, NewFilename: name})
			if err != nil {
				return renamedMsg{err: err}
			}
			return renamedMsg{documentID: result.DocumentID, path: result.Path}
		}, "rename <new-filename>", nil
	case "web":
		return m.webCommandAction(tokens)
	}
	return nil, strings.Join(tokens, " "), fmt.Errorf("unknown command %q", tokens[0])
}

func topicNames(topics []membox.TopicView) string {
	names := make([]string, 0, len(topics))
	for _, topic := range topics {
		names = append(names, topic.Name)
	}
	return strings.Join(names, ", ")
}

func (m Model) updateConfigPanel(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "enter", "q":
		m.configVisible = false
		return m, nil
	case "up", "k":
		if m.configSelected > 0 {
			m.configSelected--
		}
		return m, nil
	case "down", "j":
		if m.configSelected+1 < len(m.settings) {
			m.configSelected++
		}
		return m, nil
	case "left", "h":
		return m, m.cycleSetting(-1)
	case "right", "l", "space", " ":
		return m, m.cycleSetting(1)
	case "o":
		if !m.web.running() {
			return m, nil
		}
		return m, tea.ExecProcess(openCommand(m.ctx, m.launcher, m.web.status.URL), func(err error) tea.Msg { return openWebMsg{url: m.web.status.URL, err: err} })
	case "x":
		if m.web.running() {
			return m, tea.Batch(m.spinner.Tick, webStopCmd(m.ctx, m.app))
		}
		m.web.starting = true
		return m, tea.Batch(m.spinner.Tick, webEnsureCmd(m.ctx, m.app))
	}
	return m, nil
}

// toggleSearchScope flips name ⇄ content. When entering content with only
// name tags that match nothing (body-only terms), re-scope those tags to full.
func (m *Model) toggleSearchScope() {
	if m.searchMode == searchModeName {
		m.searchMode = searchModeFull
		onlyName := len(m.textFilters) > 0
		for _, filter := range m.textFilters {
			if filter.Mode != searchModeName {
				onlyName = false
				break
			}
		}
		if onlyName {
			m.refreshFilter()
			if len(m.filtered) == 0 {
				for i := range m.textFilters {
					m.textFilters[i].Mode = searchModeFull
				}
			}
		}
		return
	}
	m.searchMode = searchModeName
}

// updateFilterOptions handles keys while the filter-options panel is open:
// ↑↓ picks a row (match / case / scope), ←→/space toggles, esc returns to input.
// Every change is applied immediately and the status bar reflects it.
func (m Model) updateFilterOptions(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	const rows = 3
	changed := false
	switch msg.String() {
	case "esc", "enter", "q":
		m.filterOptionsVisible = false
		return m, nil
	case "up", "k":
		if m.filterOptionsSelected > 0 {
			m.filterOptionsSelected--
		}
		return m, nil
	case "down", "j":
		if m.filterOptionsSelected+1 < rows {
			m.filterOptionsSelected++
		}
		return m, nil
	case "left", "h", "right", "l", "space", " ":
		switch m.filterOptionsSelected {
		case 0:
			m.filterExact = !m.filterExact
		case 1:
			m.filterCase = !m.filterCase
		case 2:
			m.toggleSearchScope()
		}
		changed = true
	}
	if !changed {
		return m, nil
	}
	// Re-run the name filter so a live draft matches with the new semantics;
	// the status bar segment updates on the next render either way.
	return m, m.filterChanged(nil)
}

// openCommand builds the platform browser opener, ignoring errors until the
// returned command runs (tea.ExecProcess surfaces them there).
func openCommand(ctx context.Context, launcher host.Launcher, url string) *exec.Cmd {
	command, err := launcher.OpenCommand(ctx, url)
	if err != nil {
		return exec.Command("false")
	}
	return command
}

func (m Model) cycleSetting(direction int) tea.Cmd {
	if len(m.settings) == 0 || m.configSelected >= len(m.settings) {
		return nil
	}
	setting := m.settings[m.configSelected]
	if len(setting.Options) == 0 {
		return nil
	}
	index := 0
	for i, option := range setting.Options {
		if option == setting.Value {
			index = i
			break
		}
	}
	next := (index + direction + len(setting.Options)) % len(setting.Options)
	value := setting.Options[next]
	if value == setting.Value {
		return nil
	}
	m.loading = true
	return tea.Batch(m.spinner.Tick, setSettingCmd(m.ctx, m.app, setting.Key, value))
}

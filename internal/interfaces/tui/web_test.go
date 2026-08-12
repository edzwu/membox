package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"membox"
)

// runningWebModel returns a model whose companion is up and whose settings
// carry the given quit policy.
func runningWebModel(app *fakeApp, onExit string) Model {
	app.webRunning = true
	app.webMode = "session"
	app.webOnExit = onExit
	model := New(context.Background(), app, fakeLauncher{})
	model.width, model.height = 100, 30
	updated, _ := model.Update(settingsMsg{settings: []membox.SettingView{
		{Key: "viewer", Label: "viewer", Value: "leaf", Options: []string{"leaf", "web"}},
		{Key: "web_on_exit", Label: "web on exit", Value: onExit, Options: []string{"ask", "stop", "keep"}},
	}})
	model = updated.(Model)
	updated, _ = model.Update(webStatusMsg{view: app.webStatusView()})
	model = updated.(Model)
	return model
}

func TestModel_WebBadgeReflectsCompanionState(t *testing.T) {
	app := &fakeApp{}
	model := New(context.Background(), app, fakeLauncher{})
	model.width, model.height = 100, 30

	if badge := model.webBadge(); !strings.Contains(badge, "starting") {
		t.Fatalf("before any answer the badge should read starting: %q", badge)
	}

	updated, _ := model.Update(webStatusMsg{view: membox.WebStatusView{Running: true, Port: 8787, Tabs: 2}})
	model = updated.(Model)
	if badge := model.webBadge(); !strings.Contains(badge, ":8787") || !strings.Contains(badge, "2 tabs") || !strings.Contains(badge, "saved") {
		t.Fatalf("running badge missing port/tabs/saved: %q", badge)
	}

	updated, _ = model.Update(webStatusMsg{view: membox.WebStatusView{Running: true, Port: 8787, Tabs: 1, DirtyTabs: 1}})
	model = updated.(Model)
	if badge := model.webBadge(); !strings.Contains(badge, "1 unsaved") {
		t.Fatalf("dirty badge should surface unsaved tabs: %q", badge)
	}

	updated, _ = model.Update(webStatusMsg{view: membox.WebStatusView{Running: false}})
	model = updated.(Model)
	if badge := model.webBadge(); !strings.Contains(badge, "off") {
		t.Fatalf("stopped badge should read off: %q", badge)
	}

	updated, _ = model.Update(webStatusMsg{err: errors.New("spawn failed")})
	model = updated.(Model)
	if badge := model.webBadge(); !strings.Contains(badge, "unavailable") {
		t.Fatalf("failed badge should read unavailable: %q", badge)
	}
}

func TestModel_WebRefreshRetriesAfterTransientFailureOrMissedProbe(t *testing.T) {
	app := &fakeApp{}
	model := runningWebModel(app, "stop")
	sequence := model.web.sequence

	if next := model.applyWebStatus(webStatusMsg{
		err: errors.New("temporary probe failure"), sequence: sequence, refresh: true,
	}); next == nil {
		t.Fatal("transient WebStatus failure terminated the refresh loop")
	}
	if model.web.err == nil {
		t.Fatal("transient failure was not reflected in web state")
	}

	if next := model.applyWebStatus(webStatusMsg{
		view: membox.WebStatusView{Running: false}, sequence: sequence, refresh: true,
	}); next == nil {
		t.Fatal("one missed probe terminated the refresh loop")
	}
	if model.web.err != nil || model.web.running() {
		t.Fatalf("missed probe state = running %v error %v", model.web.running(), model.web.err)
	}

	if next := model.applyWebStatus(webStatusMsg{sequence: sequence - 1, refresh: true}); next != nil {
		t.Fatal("stale refresh generation created a duplicate loop")
	}
}

func TestMaintainWebLeaseRunsIndependentlyUntilContextEnds(t *testing.T) {
	app := &fakeApp{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		maintainWebLease(ctx, app, "tui-controller", 5*time.Millisecond)
		close(done)
	}()
	time.Sleep(18 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("lease loop did not stop with its context")
	}
	if app.webLeaseCalls < 2 {
		t.Fatalf("lease renewals = %d, want at least 2", app.webLeaseCalls)
	}
}

func TestModel_QuitWithAskPolicyShowsPrompt(t *testing.T) {
	app := &fakeApp{webTabs: 2, webDirtyTabs: 1}
	model := runningWebModel(app, "ask")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	model = updated.(Model)
	if !model.webQuitPrompt {
		t.Fatal("ctrl+d with ask policy and a running companion must open the prompt")
	}
	view := model.View()
	if !strings.Contains(view, "Keep web running and quit") || !strings.Contains(view, "Stop web and quit") {
		t.Fatalf("prompt missing choices: %q", view)
	}
	if !strings.Contains(view, "2 browser tabs") || !strings.Contains(view, "unsaved notes") {
		t.Fatalf("prompt should report tabs and unsaved notes: %q", view)
	}

	// esc cancels without quitting or stopping.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.webQuitPrompt {
		t.Fatal("esc should close the prompt")
	}
	if app.webStopCalls != 0 {
		t.Fatal("esc must not stop the companion")
	}
}

func TestModel_QuitPromptKeepPromotesLifecycle(t *testing.T) {
	app := &fakeApp{webTabs: 1}
	model := runningWebModel(app, "ask")

	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	model = updated.(Model)
	updated, command = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	model = updated.(Model)
	if command == nil {
		t.Fatal("keep choice must schedule a lifecycle command")
	}
	result, ok := command().(webQuitResultMsg)
	if !ok || result.err != nil {
		t.Fatalf("keep choice should report success, got %#v", result)
	}
	updated, quitCommand := model.Update(result)
	model = updated.(Model)
	if quitCommand == nil {
		t.Fatal("successful keep must schedule quit")
	}
	if app.webLifecycle != "keep" {
		t.Fatalf("keep choice must promote lifecycle, got %q", app.webLifecycle)
	}
	if app.webStopCalls != 0 {
		t.Fatal("keep choice must not stop the companion")
	}
}

func TestModel_QuitPromptStopStopsCompanion(t *testing.T) {
	app := &fakeApp{webTabs: 1}
	model := runningWebModel(app, "ask")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	model = updated.(Model)
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	model = updated.(Model)
	if command == nil {
		t.Fatal("stop choice must schedule a stop command")
	}
	result, ok := command().(webQuitResultMsg)
	if !ok || result.err != nil {
		t.Fatalf("stop choice should report success, got %#v", result)
	}
	updated, quitCommand := model.Update(result)
	model = updated.(Model)
	if quitCommand == nil {
		t.Fatal("successful stop must schedule quit")
	}
	if app.webStopCalls != 1 {
		t.Fatalf("stop choice must stop the companion once, got %d", app.webStopCalls)
	}
}

func TestModel_QuitWithStopPolicyStopsWithoutPrompt(t *testing.T) {
	app := &fakeApp{webTabs: 1}
	model := runningWebModel(app, "stop")

	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	model = updated.(Model)
	if model.webQuitPrompt {
		t.Fatal("stop policy must not open the prompt")
	}
	if command == nil {
		t.Fatal("stop policy must schedule a lease release")
	}
	result, ok := command().(webQuitResultMsg)
	if !ok || result.err != nil {
		t.Fatalf("stop policy should release successfully, got %#v", result)
	}
	updated, quitCommand := model.Update(result)
	model = updated.(Model)
	if quitCommand == nil {
		t.Fatal("successful release must schedule quit")
	}
	if app.webStopCalls != 0 || app.webReleaseCalls != 1 {
		t.Fatalf("quit policy must release only this TUI: stop=%d release=%d", app.webStopCalls, app.webReleaseCalls)
	}
}

func TestModel_QuitWithKeepPolicyQuitsSilently(t *testing.T) {
	app := &fakeApp{webTabs: 1}
	model := runningWebModel(app, "keep")

	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	model = updated.(Model)
	if model.webQuitPrompt {
		t.Fatal("keep policy must not open the prompt")
	}
	if command == nil {
		t.Fatal("keep policy must promote the companion before quitting")
	}
	result, ok := command().(webQuitResultMsg)
	if !ok || result.err != nil {
		t.Fatalf("keep policy should report success, got %#v", result)
	}
	updated, quitCommand := model.Update(result)
	model = updated.(Model)
	if quitCommand == nil {
		t.Fatal("successful keep must schedule quit")
	}
	if app.webStopCalls != 0 {
		t.Fatal("keep policy must not stop the companion")
	}
	if app.webLifecycle != "keep" {
		t.Fatalf("keep policy must promote a session companion, got %q", app.webLifecycle)
	}
}

func TestModel_QuitStopFailureRestoresPrompt(t *testing.T) {
	app := &fakeApp{webTabs: 1, webStopErr: errors.New("stop denied")}
	model := runningWebModel(app, "ask")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	model = updated.(Model)
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	model = updated.(Model)
	result := command().(webQuitResultMsg)
	updated, quitCommand := model.Update(result)
	model = updated.(Model)
	if quitCommand != nil {
		t.Fatal("failed stop must not quit")
	}
	if !model.webQuitPrompt || model.err == nil || !strings.Contains(model.err.Error(), "stop denied") {
		t.Fatalf("failed stop must restore prompt and show error: prompt=%v err=%v", model.webQuitPrompt, model.err)
	}
}

func TestModel_QuitKeepFailureRestoresPrompt(t *testing.T) {
	app := &fakeApp{webTabs: 1, webLifecycleErr: errors.New("promotion denied")}
	model := runningWebModel(app, "ask")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	model = updated.(Model)
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	model = updated.(Model)
	result := command().(webQuitResultMsg)
	updated, quitCommand := model.Update(result)
	model = updated.(Model)
	if quitCommand != nil {
		t.Fatal("failed keep promotion must not quit")
	}
	if !model.webQuitPrompt || model.err == nil || !strings.Contains(model.err.Error(), "promotion denied") {
		t.Fatalf("failed keep must restore prompt and show error: prompt=%v err=%v", model.webQuitPrompt, model.err)
	}
}

func TestModel_QuitWithoutCompanionIsImmediate(t *testing.T) {
	app := &fakeApp{}
	model := New(context.Background(), app, fakeLauncher{})
	model.width, model.height = 100, 30
	updated, _ := model.Update(webStatusMsg{view: membox.WebStatusView{Running: false}})
	model = updated.(Model)

	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	model = updated.(Model)
	if model.webQuitPrompt {
		t.Fatal("no companion means no prompt")
	}
	if command == nil {
		t.Fatal("ctrl+d must return the quit command")
	}
	if _, ok := command().(tea.QuitMsg); !ok {
		t.Fatal("no companion means a bare quit")
	}
}

func TestModel_ConfigPanelShowsWebSectionAndStopsWithX(t *testing.T) {
	app := &fakeApp{}
	model := runningWebModel(app, "ask")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = updated.(Model)
	if !model.configVisible {
		t.Fatal("ctrl+o did not open the config panel")
	}
	view := model.View()
	if !strings.Contains(view, "web reader") {
		t.Fatalf("config panel missing web section: %q", view)
	}
	if !strings.Contains(view, ":8787") {
		t.Fatalf("config panel web section missing port: %q", view)
	}

	// x stops the running companion.
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	model = updated.(Model)
	if command == nil {
		t.Fatal("x must schedule a stop command while running")
	}
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		message = batch[len(batch)-1]()
	}
	if result, ok := message.(commandResultMsg); !ok || result.err != nil {
		t.Fatalf("x should stop the companion cleanly, got %#v", message)
	}
	if app.webStopCalls != 1 {
		t.Fatalf("x must stop the companion once, got %d", app.webStopCalls)
	}
}

func TestModel_ConfigPanelXStartsStoppedCompanion(t *testing.T) {
	app := &fakeApp{}
	model := New(context.Background(), app, fakeLauncher{})
	model.width, model.height = 100, 30
	updated, _ := model.Update(webStatusMsg{view: membox.WebStatusView{Running: false}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = updated.(Model)

	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	model = updated.(Model)
	if command == nil {
		t.Fatal("x must schedule an ensure command while stopped")
	}
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		message = batch[len(batch)-1]()
	}
	if status, ok := message.(webStatusMsg); !ok || !status.view.Running {
		t.Fatalf("x should start the companion, got %#v", message)
	}
	if app.webEnsureCalls == 0 {
		t.Fatal("x must call EnsureWebCompanion")
	}
}

func TestModel_WebStatusCommandUpdatesBadge(t *testing.T) {
	app := &fakeApp{webTabs: 3}
	model := runningWebModel(app, "ask")

	model.inputVisible = true
	model.inputActive = true
	model.inputMode = inputModeCmd
	model.input.SetValue("web status")

	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if command == nil {
		t.Fatal("web status must schedule a command")
	}
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		message = batch[len(batch)-1]()
	}
	status, ok := message.(webStatusMsg)
	if !ok {
		t.Fatalf("web status should return webStatusMsg, got %T", message)
	}
	updated, _ = model.Update(status)
	model = updated.(Model)
	if badge := model.webBadge(); !strings.Contains(badge, "3 tabs") {
		t.Fatalf("badge did not pick up refreshed tabs: %q", badge)
	}
}

func TestModel_WebKeepCommandSetsSettingAndLifecycle(t *testing.T) {
	app := &fakeApp{}
	model := runningWebModel(app, "ask")

	model.inputVisible = true
	model.inputActive = true
	model.inputMode = inputModeCmd
	model.input.SetValue("web keep")

	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if command == nil {
		t.Fatal("web keep must schedule a command")
	}
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		message = batch[len(batch)-1]()
	}
	if saved, ok := message.(settingSavedMsg); !ok || saved.key != "web_on_exit" || saved.value != "keep" {
		t.Fatalf("web keep should save web_on_exit=keep, got %#v", message)
	}
	if app.webLifecycle != "keep" {
		t.Fatalf("web keep must promote a running companion, got %q", app.webLifecycle)
	}
}

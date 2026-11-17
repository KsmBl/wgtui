package ui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// drive feeds keys and resolves returned commands that produce our own
// synchronous messages (text views, op results).
func drive(m *Model, keys ...string) {
	for _, k := range keys {
		_, cmd := m.Update(key(k))
		resolve(m, cmd)
	}
}

func resolve(m *Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	switch msg := msg.(type) {
	case showTextMsg, opDoneMsg, statusMsg:
		_, c := m.Update(msg)
		resolve(m, c)
	case tea.BatchMsg:
		for _, c := range msg {
			resolve(m, c)
		}
	}
}

// TestScreenshots writes ANSI frames to $WGTUI_SCREENSHOTS (skipped otherwise).
func TestScreenshots(t *testing.T) {
	out := os.Getenv("WGTUI_SCREENSHOTS")
	if out == "" {
		t.Skip("set WGTUI_SCREENSHOTS=dir")
	}
	lipgloss.SetColorProfile(termenv.TrueColor)
	shots := map[string][]string{
		"main":       nil,
		"connection": {"down", "down"},
		"client":     {"enter", "down", "enter"},
		"network":    {"n"},
		"mode":       {"down", "e", "tab", "tab", "tab"},
		"connect":    {"c"},
	}
	for name, keys := range shots {
		s, d, err := DemoSetup()
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(s.Dir)
		m := New(s, d, true, true)
		m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
		resolve(m, m.fetchStatus())
		drive(m, keys...)
		if err := os.WriteFile(filepath.Join(out, name+".ansi"), []byte(m.View()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

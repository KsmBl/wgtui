package ui

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/KsmBl/wgtui/internal/store"
)

// recorder is a runtime that records what wg-quick would have been given.
type recorder struct {
	Demo
	downConf string
	downErr  error
	upCalls  int
}

func (r *recorder) Down(n, path string) error {
	data, _ := os.ReadFile(path)
	r.downConf = string(data)
	r.up[n] = false
	return r.downErr
}

func (r *recorder) Up(n, path string) error {
	r.upCalls++
	r.up[n] = true
	return nil
}

func editMode(t *testing.T, downErr error) (*recorder, *Model) {
	t.Helper()
	s, err := store.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateNetwork(store.NetworkOpts{Name: "wg0", Address: "10.100.0.1/24", ListenPort: 51820, Mode: store.ModeCustom}); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{Demo: *NewDemo(s, "wg0"), downErr: downErr}
	m := New(s, rec, true, false)
	// e (edit), move to "Client visibility" and switch custom -> mesh, save.
	drive(m, "e", "tab", "tab", "tab", "right")
	if m.form == nil {
		t.Fatal("edit form not open")
	}
	_, cmd := m.Update(key("ctrl+s"))
	resolve(m, cmd)
	return rec, m
}

// Regression: switching custom -> mesh on a running network used the new
// config (with a PostDown for a rule never added) to stop the interface,
// iptables failed, and the interface stayed down.
func TestRestartUsesOldConfig(t *testing.T) {
	rec, m := editMode(t, nil)
	if rec.upCalls != 1 {
		t.Fatalf("interface not started again (up calls %d), msg %q", rec.upCalls, m.msg)
	}
	if strings.Contains(rec.downConf, "iptables") {
		t.Errorf("stopped with the new config:\n%s", rec.downConf)
	}
	if !strings.Contains(rec.downConf, "ListenPort = 51820") {
		t.Errorf("old config not used for down:\n%s", rec.downConf)
	}
	data, _ := os.ReadFile(m.store.Get("wg0").Path)
	if !strings.Contains(string(data), "PostDown = iptables -D FORWARD -i %i -o %i -j ACCEPT || true") {
		t.Errorf("new config lacks tolerant PostDown:\n%s", data)
	}
}

func TestRestartStartsEvenIfStopFails(t *testing.T) {
	rec, m := editMode(t, errors.New("iptables: Bad rule"))
	if rec.upCalls != 1 {
		t.Fatal("interface left down after a failing stop")
	}
	if !m.msgErr || !strings.Contains(m.msg, "restarted, but") {
		t.Errorf("expected warning, got %q", m.msg)
	}
}

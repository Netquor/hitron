package ui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Netquor/hitron/cli/internal/hitron"
)

func typed(m model, s string) model {
	for _, r := range s {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(model)
	}
	return m
}

func enter(m model) (model, tea.Cmd) {
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return next.(model), cmd
}

func TestRebootNeedsTypedConfirmation(t *testing.T) {
	calls := 0
	m := model{
		ctx: context.Background(), history: NewHistory(), width: 100, height: 30,
		snap:   &hitron.Snapshot{},
		reboot: func(context.Context, string, string) (string, error) { calls++; return "ok", nil },
	}
	m = m.openDialog()
	if m.dlg != dlgUser {
		t.Fatalf("stage = %v, want user", m.dlg)
	}
	m = typed(m, "admin")
	m, _ = enter(m)
	m = typed(m, "pw")
	m, _ = enter(m)
	if m.dlg != dlgConfirm {
		t.Fatalf("stage = %v, want confirm", m.dlg)
	}
	m = typed(m, "reb")
	if m, cmd := enter(m); cmd != nil || m.dlg != dlgConfirm || calls != 0 {
		t.Fatal("partial confirmation must not send")
	}
	m = typed(m, "oot")
	m, cmd := enter(m)
	if cmd == nil || m.dlg != dlgRunning {
		t.Fatal("full confirmation should start the reboot")
	}
	if msg := cmd().(rebootMsg); msg.err != nil || calls != 1 {
		t.Fatalf("reboot result %+v calls %d", msg, calls)
	}
}

func TestRebootPresetLoginStillConfirms(t *testing.T) {
	m := model{snap: &hitron.Snapshot{}, preUser: "u", prePass: "p",
		reboot: func(context.Context, string, string) (string, error) { return "", nil }}
	m = m.openDialog()
	if m.dlg != dlgConfirm {
		t.Fatalf("stage = %v, want confirm", m.dlg)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if m.dlg != dlgNone || m.fPass != nil {
		t.Fatal("Esc should close and wipe the dialog")
	}
}

func TestNoRebootWithoutHook(t *testing.T) {
	m := model{snap: &hitron.Snapshot{}}
	if m.openDialog().dlg != dlgNone {
		t.Fatal("dialog must not open without a reboot hook")
	}
}

func TestCodewordCell(t *testing.T) {
	old := Color
	Color = false
	defer func() { Color = old }()
	if got := codewordCell(true, 14, 640); !strings.Contains(got, "14/640 97.86%") {
		t.Errorf("got %q", got)
	}
	if got := codewordCell(true, 5, 0); !strings.Contains(got, "5/0 0.00%") {
		t.Errorf("got %q", got)
	}
	if got := codewordCell(true, 9_000_000, 1); !strings.Contains(got, "<0.01%") {
		t.Errorf("got %q", got)
	}
}

func TestNoLoginRebootSkipsCredentials(t *testing.T) {
	m := model{snap: &hitron.Snapshot{}, noLogin: true,
		reboot: func(context.Context, string, string) (string, error) { return "", nil }}
	m = m.openDialog()
	if m.dlg != dlgConfirm {
		t.Fatalf("stage = %v, want confirm straight away", m.dlg)
	}
	if _, cmd := enter(m); cmd != nil {
		t.Fatal("must still require the typed word")
	}
}

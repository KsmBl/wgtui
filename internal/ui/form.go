package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// field is a text input or, when options is set, a selector.
type field struct {
	key     string
	label   string
	help    string
	input   textinput.Model
	options []string
	opt     int
	optHelp func(string) string
}

func (f *field) value() string {
	if f.options != nil {
		return f.options[f.opt]
	}
	return strings.TrimSpace(f.input.Value())
}

type formValues map[string]string

// form is a modal dialog with fields. submit returns an error to keep the
// form open, or a command to run after closing it.
type form struct {
	title  string
	intro  string
	fields []*field
	focus  int
	err    string
	submit func(formValues) (tea.Cmd, error)
}

func newForm(title, intro string, submit func(formValues) (tea.Cmd, error)) *form {
	return &form{title: title, intro: intro, submit: submit}
}

func (f *form) text(key, label, value, placeholder, help string) *form {
	in := textinput.New()
	in.Prompt = ""
	in.SetValue(value)
	in.Placeholder = placeholder
	in.PlaceholderStyle = sMuted
	in.TextStyle = sText
	in.Cursor.Style = sAccent
	in.Width = 46
	in.CharLimit = 256
	f.fields = append(f.fields, &field{key: key, label: label, help: help, input: in})
	return f
}

func (f *form) choice(key, label string, options []string, value string, optHelp func(string) string) *form {
	fl := &field{key: key, label: label, options: options, optHelp: optHelp}
	for i, o := range options {
		if o == value {
			fl.opt = i
		}
	}
	f.fields = append(f.fields, fl)
	return f
}

func (f *form) init() tea.Cmd {
	f.focus = 0
	return f.setFocus()
}

func (f *form) setFocus() tea.Cmd {
	var cmd tea.Cmd
	for i, fl := range f.fields {
		if fl.options != nil {
			continue
		}
		if i == f.focus {
			cmd = fl.input.Focus()
		} else {
			fl.input.Blur()
		}
	}
	return cmd
}

func (f *form) values() formValues {
	v := formValues{}
	for _, fl := range f.fields {
		v[fl.key] = fl.value()
	}
	return v
}

// update handles a key; done is true when the form should close.
func (f *form) update(msg tea.Msg) (cmd tea.Cmd, done bool) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		if f.focus < len(f.fields) && f.fields[f.focus].options == nil {
			var c tea.Cmd
			f.fields[f.focus].input, c = f.fields[f.focus].input.Update(msg)
			return c, false
		}
		return nil, false
	}
	cur := f.fields[f.focus]
	switch km.String() {
	case "esc":
		return nil, true
	case "tab", "down":
		f.focus = (f.focus + 1) % len(f.fields)
		return f.setFocus(), false
	case "shift+tab", "up":
		f.focus = (f.focus - 1 + len(f.fields)) % len(f.fields)
		return f.setFocus(), false
	case "enter", "ctrl+s":
		if km.String() == "enter" && f.focus < len(f.fields)-1 {
			f.focus++
			return f.setFocus(), false
		}
		c, err := f.submit(f.values())
		if err != nil {
			f.err = err.Error()
			return nil, false
		}
		return c, true
	}
	if cur.options != nil {
		switch km.String() {
		case "left", "h":
			cur.opt = (cur.opt - 1 + len(cur.options)) % len(cur.options)
		case "right", "l", " ":
			cur.opt = (cur.opt + 1) % len(cur.options)
		}
		return nil, false
	}
	f.err = ""
	var c tea.Cmd
	cur.input, c = cur.input.Update(msg)
	return c, false
}

func (f *form) view(width int) string {
	var b strings.Builder
	b.WriteString(sHeader.Render(f.title) + "\n")
	if f.intro != "" {
		b.WriteString(sMuted.Width(min(64, width-8)).Render(f.intro) + "\n")
	}
	b.WriteString("\n")
	label := lipgloss.NewStyle().Width(18)
	for i, fl := range f.fields {
		focused := i == f.focus
		l := label.Foreground(cMuted).Render(fl.label)
		marker := "  "
		if focused {
			l = label.Foreground(cAccent).Bold(true).Render(fl.label)
			marker = sAccent.Render("▸ ")
		}
		var val string
		if fl.options != nil {
			var parts []string
			for j, o := range fl.options {
				if j == fl.opt {
					st := sBold.Background(lipgloss.Color("#283457")).Padding(0, 1)
					if focused {
						st = st.Foreground(cAccent)
					}
					parts = append(parts, st.Render(o))
				} else {
					parts = append(parts, sMuted.Padding(0, 1).Render(o))
				}
			}
			val = strings.Join(parts, " ")
		} else {
			val = fl.input.View()
		}
		b.WriteString(marker + l + val + "\n")
		help := fl.help
		if fl.options != nil && fl.optHelp != nil {
			help = fl.optHelp(fl.value())
		}
		if focused && help != "" {
			b.WriteString(strings.Repeat(" ", 20) + sMuted.Italic(true).Render(help) + "\n")
		}
	}
	b.WriteString("\n")
	if f.err != "" {
		b.WriteString(sRed.Render("✗ "+f.err) + "\n\n")
	}
	b.WriteString(sMuted.Render("tab/↑↓ move · ←→ choose · enter next/save · ctrl+s save · esc cancel"))
	return sDialog.Render(b.String())
}

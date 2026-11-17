package ui

import "github.com/charmbracelet/lipgloss"

var (
	cAccent = lipgloss.Color("#7AA2F7")
	cGreen  = lipgloss.Color("#9ECE6A")
	cYellow = lipgloss.Color("#E0AF68")
	cRed    = lipgloss.Color("#F7768E")
	cMuted  = lipgloss.Color("#737AA2")
	cText   = lipgloss.Color("#C0CAF5")
	cPurple = lipgloss.Color("#BB9AF7")
	cBorder = lipgloss.Color("#3B4261")

	sTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1A1B26")).Background(cAccent).Padding(0, 1)
	sMuted  = lipgloss.NewStyle().Foreground(cMuted)
	sText   = lipgloss.NewStyle().Foreground(cText)
	sBold   = lipgloss.NewStyle().Foreground(cText).Bold(true)
	sAccent = lipgloss.NewStyle().Foreground(cAccent)
	sGreen  = lipgloss.NewStyle().Foreground(cGreen)
	sYellow = lipgloss.NewStyle().Foreground(cYellow)
	sRed    = lipgloss.NewStyle().Foreground(cRed)
	sPurple = lipgloss.NewStyle().Foreground(cPurple)
	sLabel  = lipgloss.NewStyle().Foreground(cMuted).Width(13)
	sSel    = lipgloss.NewStyle().Background(lipgloss.Color("#283457")).Foreground(cText).Bold(true)
	sKey    = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sHeader = lipgloss.NewStyle().Foreground(cPurple).Bold(true)

	sPane    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cBorder).Padding(0, 1)
	sFocused = sPane.BorderForeground(cAccent)
	sDialog  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cAccent).Padding(1, 2)
)

package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	qrcode "github.com/skip2/go-qrcode"
)

var sQR = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#000000"))

// renderQR draws a QR code with half blocks, two modules per character
// row. Colors are forced (white on black) so it scans on any theme.
func renderQR(content string) string {
	q, err := qrcode.New(content, qrcode.Low)
	if err != nil {
		return sRed.Render("QR: " + err.Error())
	}
	bm := q.Bitmap()
	// go-qrcode adds a 4 module quiet zone; 2 is enough on screen.
	trim := 2
	bm = bm[trim : len(bm)-trim]
	for i := range bm {
		bm[i] = bm[i][trim : len(bm[i])-trim]
	}
	var lines []string
	for y := 0; y < len(bm); y += 2 {
		var sb strings.Builder
		for x := range bm[y] {
			top := !bm[y][x] // light module
			bottom := true
			if y+1 < len(bm) {
				bottom = !bm[y+1][x]
			}
			switch {
			case top && bottom:
				sb.WriteRune('█')
			case top:
				sb.WriteRune('▀')
			case bottom:
				sb.WriteRune('▄')
			default:
				sb.WriteRune(' ')
			}
		}
		lines = append(lines, sQR.Render(sb.String()))
	}
	return strings.Join(lines, "\n")
}

// wgtui is a terminal UI to manage WireGuard networks and connections.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/KsmBl/wgtui/internal/store"
	"github.com/KsmBl/wgtui/internal/ui"
)

var version = "dev"

func main() {
	dir := flag.String("dir", "/etc/wireguard", "WireGuard config directory")
	demo := flag.Bool("demo", false, "run with example data and a simulated system (no root needed)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "wgtui %s — manage WireGuard networks and connections\n\nUsage: sudo wgtui [flags]\n\n", version)
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("wgtui", version)
		return
	}

	var rt ui.Runtime = ui.System{}
	var s *store.Store
	var err error
	if *demo {
		var d *ui.Demo
		s, d, err = ui.DemoSetup()
		if err == nil {
			defer os.RemoveAll(s.Dir)
			rt = d
		}
	} else {
		if err = os.MkdirAll(*dir, 0o700); err == nil {
			s, err = store.Load(*dir)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "wgtui:", err)
		if os.IsPermission(err) || os.Geteuid() != 0 {
			fmt.Fprintln(os.Stderr, "hint: run with sudo, or try `wgtui --demo`")
		}
		os.Exit(1)
	}
	m := ui.New(s, rt, os.Geteuid() == 0, *demo)
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "wgtui:", err)
		os.Exit(1)
	}
}

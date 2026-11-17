// Package ui implements the wgtui terminal interface.
package ui

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/KsmBl/wgtui/internal/store"
	"github.com/KsmBl/wgtui/internal/wg"
	"github.com/KsmBl/wgtui/internal/wgconf"
)

const systemDir = "/etc/wireguard"

type focus int

const (
	focusList focus = iota
	focusPeers
)

type (
	tickMsg   time.Time
	statusMsg struct {
		status map[string]*wg.IfaceStatus
		err    error
	}
	opDoneMsg struct {
		ok  string
		err error
	}
	showTextMsg textView
)

type textView struct {
	title  string
	body   string // shown as-is
	qr     string // content to encode, optional
	note   string
	showQR bool
	scroll int
	export func() // optional "w" action
	iface  *store.Iface
	peer   string
}

type confirmDialog struct {
	text   string
	action func() tea.Cmd
}

// Model is the Bubble Tea model.
type Model struct {
	store   *store.Store
	rt      Runtime
	root    bool
	demo    bool
	status  map[string]*wg.IfaceStatus
	stErr   error
	auto    map[string]bool
	width   int
	height  int
	sel     int
	peerSel int
	peerOff int
	focus   focus
	form    *form
	confirm *confirmDialog
	text    *textView
	help    bool
	msg     string
	msgErr  bool
	busy    string
}

// New creates the UI model.
func New(s *store.Store, rt Runtime, root, demo bool) *Model {
	m := &Model{store: s, rt: rt, root: root, demo: demo, auto: map[string]bool{}}
	m.refreshAutostart()
	if len(s.Errors) > 0 {
		m.setErr(fmt.Errorf("skipped %d unreadable config(s): %s", len(s.Errors), strings.Join(s.Errors, "; ")))
	} else if n := len(s.Ifaces); n > 0 {
		imported := 0
		for _, i := range s.Ifaces {
			if !i.HasMeta {
				imported++
			}
		}
		m.msg = fmt.Sprintf("loaded %d interface(s) from %s", n, s.Dir)
		if demo {
			m.msg = "demo mode: example data, nothing on your system is changed"
		}
		if imported > 0 {
			m.msg += fmt.Sprintf(", %d existing config(s) detected (left untouched until you change them)", imported)
		}
	} else {
		m.msg = "no WireGuard configs yet — press n to create a network or c to connect to a server"
	}
	return m
}

func (m *Model) refreshAutostart() {
	if m.store.Dir != systemDir && !m.demo {
		return
	}
	for _, i := range m.store.Ifaces {
		m.auto[i.Name] = m.rt.Autostart(i.Name)
	}
}

func (m *Model) setErr(err error) { m.msg, m.msgErr = err.Error(), true }
func (m *Model) setOK(s string)   { m.msg, m.msgErr = s, false }

// Init starts the status polling.
func (m *Model) Init() tea.Cmd { return tea.Batch(m.fetchStatus(), tick()) }

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) fetchStatus() tea.Cmd {
	rt, root := m.rt, m.root || m.demo
	return func() tea.Msg {
		if !root {
			return statusMsg{}
		}
		st, err := rt.Status()
		return statusMsg{st, err}
	}
}

func (m *Model) op(label string, fn func() error, ok string) tea.Cmd {
	m.busy = label
	return func() tea.Msg { return opDoneMsg{ok: ok, err: fn()} }
}

func (m *Model) current() *store.Iface {
	if m.sel >= 0 && m.sel < len(m.store.Ifaces) {
		return m.store.Ifaces[m.sel]
	}
	return nil
}

func (m *Model) currentPeer() *wgconf.Section {
	i := m.current()
	if i == nil || m.peerSel < 0 || m.peerSel >= len(i.File.Peers) {
		return nil
	}
	return i.File.Peers[m.peerSel]
}

func (m *Model) isUp(i *store.Iface) bool { return m.rt.IsUp(i.Name) }

// applyPeers pushes peer changes into a running interface.
func (m *Model) applyPeers(i *store.Iface, ok string) tea.Cmd {
	if !m.isUp(i) {
		m.setOK(ok)
		return nil
	}
	return m.op("applying to "+i.Name, func() error { return m.rt.Sync(i.Name, i.Path) }, ok+" (applied live)")
}

// restart re-applies firewall rules of a running interface.
func (m *Model) restart(i *store.Iface, ok string) tea.Cmd {
	if !m.isUp(i) {
		m.setOK(ok)
		return nil
	}
	return m.op("restarting "+i.Name, func() error {
		if err := m.rt.Down(i.Name, i.Path); err != nil {
			return err
		}
		return m.rt.Up(i.Name, i.Path)
	}, ok+" ("+i.Name+" restarted)")
}

// Update handles messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		return m, tea.Batch(m.fetchStatus(), tick())
	case statusMsg:
		m.status, m.stErr = msg.status, msg.err
		return m, nil
	case opDoneMsg:
		m.busy = ""
		if msg.err != nil {
			m.setErr(msg.err)
		} else if msg.ok != "" {
			m.setOK(msg.ok)
		}
		m.refreshAutostart()
		return m, m.fetchStatus()
	case showTextMsg:
		tv := textView(msg)
		m.text = &tv
		return m, nil
	}

	if km, ok := msg.(tea.KeyMsg); ok && km.String() == "ctrl+c" {
		return m, tea.Quit
	}
	switch {
	case m.form != nil:
		cmd, done := m.form.update(msg)
		if done {
			m.form = nil
		}
		return m, cmd
	case m.confirm != nil:
		if km, ok := msg.(tea.KeyMsg); ok {
			c := m.confirm
			m.confirm = nil
			if km.String() == "y" || km.String() == "Y" {
				return m, c.action()
			}
			m.setOK("cancelled")
		}
		return m, nil
	case m.text != nil:
		return m, m.updateText(msg)
	case m.help:
		if _, ok := msg.(tea.KeyMsg); ok {
			m.help = false
		}
		return m, nil
	}
	if km, ok := msg.(tea.KeyMsg); ok {
		return m, m.handleKey(km.String())
	}
	return m, nil
}

func (m *Model) updateText(msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	t := m.text
	switch km.String() {
	case "esc", "q", "enter":
		m.text = nil
	case "t":
		if t.qr != "" {
			t.showQR = !t.showQR
		}
	case "up", "k":
		if t.scroll > 0 {
			t.scroll--
		}
	case "down", "j":
		t.scroll++
	case "w":
		if t.iface != nil && t.peer != "" {
			m.text = nil
			return m.exportForm(t.iface, t.peer)
		}
	}
	return nil
}

func (m *Model) handleKey(k string) tea.Cmd {
	i := m.current()
	switch k {
	case "q":
		return tea.Quit
	case "?":
		m.help = true
	case "up", "k":
		if m.focus == focusList {
			if m.sel > 0 {
				m.sel--
				m.peerSel, m.peerOff = 0, 0
			}
		} else if m.peerSel > 0 {
			m.peerSel--
		}
	case "down", "j":
		if m.focus == focusList {
			if m.sel < len(m.store.Ifaces)-1 {
				m.sel++
				m.peerSel, m.peerOff = 0, 0
			}
		} else if i != nil && m.peerSel < len(i.File.Peers)-1 {
			m.peerSel++
		}
	case "tab", "right", "l", "left", "h", "shift+tab":
		if m.focus == focusList && i != nil && len(i.File.Peers) > 0 && k != "left" && k != "h" {
			m.focus = focusPeers
		} else if m.focus == focusPeers && k != "right" && k != "l" {
			m.focus = focusList
		}
	case "enter":
		if m.focus == focusList {
			if i != nil && len(i.File.Peers) > 0 {
				m.focus = focusPeers
			}
		} else {
			return m.showPeer()
		}
	case "esc":
		m.focus = focusList
	case "r":
		if err := m.store.Reload(); err != nil {
			m.setErr(err)
		} else {
			m.clampSel()
			m.refreshAutostart()
			m.setOK(fmt.Sprintf("reloaded %d interface(s) from %s", len(m.store.Ifaces), m.store.Dir))
		}
		return m.fetchStatus()
	case "n":
		return m.networkForm(nil)
	case "c":
		return m.connectionForm(nil)
	case "i":
		return m.importForm()
	}
	if i == nil {
		return nil
	}
	switch k {
	case "u", " ":
		if m.isUp(i) {
			return m.op("stopping "+i.Name, func() error { return m.rt.Down(i.Name, i.Path) }, i.Name+" is down")
		}
		return m.op("starting "+i.Name, func() error { return m.rt.Up(i.Name, i.Path) }, i.Name+" is up")
	case "b":
		if m.store.Dir != systemDir && !m.demo {
			m.setErr(fmt.Errorf("autostart needs the config in %s (wg-quick@%s.service)", systemDir, i.Name))
			return nil
		}
		on := !m.auto[i.Name]
		state := map[bool]string{true: "enabled", false: "disabled"}[on]
		return m.op("autostart "+i.Name, func() error { return m.rt.SetAutostart(i.Name, on) }, "autostart "+state+" for "+i.Name)
	case "e":
		if m.focus == focusPeers && i.IsNetwork() {
			return m.renameForm(i)
		}
		if i.IsNetwork() {
			return m.networkForm(i)
		}
		return m.connectionForm(i)
	case "a":
		if !i.IsNetwork() {
			m.setErr(fmt.Errorf("%s is a connection; clients can only be added to networks", i.Name))
			return nil
		}
		return m.addClientForm(i)
	case "p":
		if !i.IsNetwork() {
			m.setErr(fmt.Errorf("%s is a connection; peers can only be added to networks", i.Name))
			return nil
		}
		return m.addPeerForm(i)
	case "s":
		if m.focus == focusPeers {
			return m.showPeer()
		}
		return m.showIface(i)
	case "v":
		return m.showIface(i)
	case "w":
		if p := m.currentPeer(); p != nil && m.focus == focusPeers {
			return m.exportForm(i, p.PublicKey())
		}
	case "d", "delete", "x":
		if m.focus == focusPeers {
			if p := m.currentPeer(); p != nil {
				return m.confirmDeletePeer(i, p)
			}
			return nil
		}
		return m.confirmDeleteIface(i)
	}
	return nil
}

func (m *Model) clampSel() {
	if m.sel >= len(m.store.Ifaces) {
		m.sel = len(m.store.Ifaces) - 1
	}
	if m.sel < 0 {
		m.sel = 0
	}
	if i := m.current(); i != nil && m.peerSel >= len(i.File.Peers) {
		m.peerSel = len(i.File.Peers) - 1
	}
	if m.peerSel < 0 {
		m.peerSel = 0
	}
	if i := m.current(); i == nil || len(i.File.Peers) == 0 {
		m.focus = focusList
	}
}

func (m *Model) selectIface(i *store.Iface) {
	for n, x := range m.store.Ifaces {
		if x == i {
			m.sel = n
		}
	}
	m.peerSel, m.peerOff, m.focus = 0, 0, focusList
}

func (m *Model) openForm(f *form) tea.Cmd {
	m.form = f
	return f.init()
}

// ---------- forms ----------

func (m *Model) freeName() string {
	for n := 0; ; n++ {
		name := "wg" + strconv.Itoa(n)
		if _, err := os.Stat(filepath.Join(m.store.Dir, name+".conf")); err != nil {
			return name
		}
	}
}

func (m *Model) freeSubnet() (string, int) {
	usedNet := map[netip.Prefix]bool{}
	usedPort := map[int]bool{}
	for _, i := range m.store.Ifaces {
		if s, ok := i.Subnet(); ok {
			usedNet[netip.PrefixFrom(s.Addr(), 24).Masked()] = true
		}
		usedPort[i.ListenPort()] = true
	}
	addr := "10.8.0.1/24"
	for x := 8; x < 250; x++ {
		p := netip.MustParsePrefix(fmt.Sprintf("10.%d.0.0/24", x))
		if !usedNet[p] {
			addr = fmt.Sprintf("10.%d.0.1/24", x)
			break
		}
	}
	port := 51820
	for usedPort[port] {
		port++
	}
	return addr, port
}

func atoiDefault(s string, def int) (int, error) {
	if strings.TrimSpace(s) == "" {
		return def, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", s)
	}
	return n, nil
}

func (m *Model) networkForm(existing *store.Iface) tea.Cmd {
	addr, port := m.freeSubnet()
	o := store.NetworkOpts{Name: m.freeName(), Address: addr, ListenPort: port, Mode: store.ModeMesh,
		OutIface: wg.DefaultRouteIface(), Keepalive: 25}
	title := "New network"
	intro := "This machine becomes the server (hub). Clients you add get their own config file / QR code."
	if existing != nil {
		o = existing.NetworkOpts()
		if o.OutIface == "" {
			o.OutIface = wg.DefaultRouteIface()
		}
		title = "Edit network " + existing.Name
		intro = ""
		if !existing.HasMeta {
			intro = "Detected existing config. Your PostUp/PostDown rules are kept; wgtui only adds its own rules in modes other than custom."
		}
	}
	yesno := map[bool]string{true: "yes", false: "no"}
	f := newForm(title, intro, func(v formValues) (tea.Cmd, error) {
		port, err := atoiDefault(v["port"], 51820)
		if err != nil {
			return nil, err
		}
		ka, err := atoiDefault(v["keepalive"], 0)
		if err != nil {
			return nil, err
		}
		no := store.NetworkOpts{Name: v["name"], Address: v["address"], ListenPort: port, Endpoint: v["endpoint"],
			DNS: v["dns"], Mode: v["mode"], FullTunnel: v["full"] == "yes", OutIface: v["out"], Keepalive: ka}
		if existing != nil {
			no.Name = existing.Name
			if err := existing.UpdateNetwork(no); err != nil {
				return nil, err
			}
			return m.restart(existing, "saved "+existing.Name), nil
		}
		i, err := m.store.CreateNetwork(no)
		if err != nil {
			return nil, err
		}
		m.selectIface(i)
		m.setOK(fmt.Sprintf("network %s created — press a to add clients, u to start it", i.Name))
		return nil, nil
	})
	if existing == nil {
		f.text("name", "Interface name", o.Name, "wg0", "becomes /etc/wireguard/<name>.conf")
	}
	f.text("address", "Server address", o.Address, "10.8.0.1/24", "server IP and size of the virtual network").
		text("port", "Listen port", strconv.Itoa(o.ListenPort), "51820", "UDP port, must be reachable from clients").
		text("endpoint", "Public endpoint", o.Endpoint, "vpn.example.com", "public IP or DNS name clients connect to (port optional)").
		choice("mode", "Client visibility", store.Modes, o.Mode, store.ModeDescription).
		choice("full", "Route all traffic", []string{"no", "yes"}, yesno[o.FullTunnel], func(s string) string {
			if s == "yes" {
				return "clients send all internet traffic through the server (NAT)"
			}
			return "clients only route the VPN through the tunnel (split tunnel)"
		}).
		text("out", "NAT interface", o.OutIface, "eth0", "uplink used for NAT when routing all traffic").
		text("dns", "Client DNS", o.DNS, "optional, e.g. 1.1.1.1", "DNS servers pushed into client configs").
		text("keepalive", "Keepalive (s)", strconv.Itoa(o.Keepalive), "25", "client PersistentKeepalive, 0 = off (25 helps behind NAT)")
	return m.openForm(f)
}

func (m *Model) connectionForm(existing *store.Iface) tea.Cmd {
	o := store.ConnectionOpts{Name: m.freeName(), AllowedIPs: "0.0.0.0/0", Keepalive: 25}
	title := "Connect to a server"
	intro := "Create a client connection. Leave the private key empty to generate one — you will then see the public key to give to the server admin. To use a finished config file instead, press i (import)."
	if existing != nil {
		o = existing.ConnectionOpts()
		title = "Edit connection " + existing.Name
		intro = ""
	}
	f := newForm(title, intro, func(v formValues) (tea.Cmd, error) {
		ka, err := atoiDefault(v["keepalive"], 0)
		if err != nil {
			return nil, err
		}
		no := store.ConnectionOpts{Name: v["name"], Address: v["address"], PrivateKey: v["priv"], DNS: v["dns"],
			PeerName: v["peername"], PeerPublicKey: v["pub"], PresharedKey: v["psk"], Endpoint: v["endpoint"],
			AllowedIPs: v["allowed"], Keepalive: ka}
		if existing != nil {
			no.Name = existing.Name
			if no.PrivateKey == "" {
				no.PrivateKey = existing.File.Interface.Get("PrivateKey")
			}
			if err := existing.UpdateConnection(no); err != nil {
				return nil, err
			}
			return m.restart(existing, "saved "+existing.Name), nil
		}
		i, err := m.store.CreateConnection(no)
		if err != nil {
			return nil, err
		}
		m.selectIface(i)
		m.setOK("connection " + i.Name + " created — press u to connect")
		return func() tea.Msg {
			return showTextMsg{title: "Connection " + i.Name + " created",
				body: sBold.Render("Your public key:") + "\n\n  " + sGreen.Render(i.PublicKey()) + "\n\n" +
					sMuted.Render("Add this key as a peer on the server with AllowedIPs = "+no.Address+".\n"+
						"If the server is managed by wgtui: select the network there and press p.\n"+
						"Then press u here to connect."),
				qr: i.PublicKey(), note: "QR contains only the public key"}
		}, nil
	})
	if existing == nil {
		f.text("name", "Interface name", o.Name, "wg0", "becomes <config dir>/<name>.conf")
	}
	f.text("address", "Your VPN address", o.Address, "10.8.0.2/32", "address the server assigned to you").
		text("pub", "Server public key", o.PeerPublicKey, "base64 key from the server", "").
		text("endpoint", "Server endpoint", o.Endpoint, "vpn.example.com:51820", "host:port").
		text("allowed", "Allowed IPs", o.AllowedIPs, "0.0.0.0/0", "0.0.0.0/0 = everything through VPN, or e.g. 10.8.0.0/24 for the VPN only").
		text("psk", "Preshared key", o.PresharedKey, "optional", "").
		text("dns", "DNS", o.DNS, "optional, e.g. 10.8.0.1", "").
		text("peername", "Server name", o.PeerName, "optional label", "").
		text("keepalive", "Keepalive (s)", strconv.Itoa(o.Keepalive), "25", "keeps NAT mappings alive; 0 = off")
	priv := ""
	if existing != nil {
		priv = o.PrivateKey
	}
	f.text("priv", "Private key", priv, "empty = generate a new key", "only paste one if the server gave you a complete key pair")
	if existing != nil {
		f.fields[len(f.fields)-1].input.EchoMode = 2 // password
	}
	return m.openForm(f)
}

func (m *Model) importForm() tea.Cmd {
	f := newForm("Import config file", "Copies an existing wg-quick .conf (e.g. one you got from a VPN provider or a wgtui server) into "+m.store.Dir+".",
		func(v formValues) (tea.Cmd, error) {
			i, err := m.store.Import(v["path"], v["name"])
			if err != nil {
				return nil, err
			}
			m.selectIface(i)
			m.setOK(fmt.Sprintf("imported %s as %s — press u to bring it up", v["path"], i.Kind()))
			return nil, nil
		})
	f.text("path", "File", "", "~/Downloads/vpn.conf", "path of the .conf file").
		text("name", "Interface name", "", "default: file name", "max 15 characters")
	return m.openForm(f)
}

func (m *Model) addClientForm(i *store.Iface) tea.Cmd {
	next, _ := i.NextIP()
	f := newForm("Add client to "+i.Name, "Generates a key pair and a ready-to-use client config (with QR code for the mobile app).",
		func(v formValues) (tea.Cmd, error) {
			p, err := i.AddClient(v["name"], v["ip"])
			if err != nil {
				return nil, err
			}
			m.selectIface(i)
			m.peerSel = len(i.File.Peers) - 1
			m.focus = focusPeers
			apply := m.applyPeers(i, "client "+p.Name+" added")
			return tea.Batch(apply, m.showPeerCmd(i, p)), nil
		})
	f.text("name", "Client name", "", "e.g. phone, laptop", "").
		text("ip", "Address", "", next.String(), "empty = next free address")
	return m.openForm(f)
}

func (m *Model) addPeerForm(i *store.Iface) tea.Cmd {
	f := newForm("Add peer by public key", "For devices that generated their own keys (e.g. a wgtui connection). wgtui can't render a config for these.",
		func(v formValues) (tea.Cmd, error) {
			p, err := i.AddPeer(v["name"], v["pub"], v["allowed"], v["endpoint"])
			if err != nil {
				return nil, err
			}
			m.peerSel = len(i.File.Peers) - 1
			m.focus = focusPeers
			return m.applyPeers(i, "peer "+store.PeerName(p)+" added"), nil
		})
	next, _ := i.NextIP()
	f.text("name", "Name", "", "e.g. office-router", "").
		text("pub", "Public key", "", "base64", "").
		text("allowed", "Allowed IPs", "", next.String()+"/32", "empty = next free address; add subnets behind the peer for site-to-site").
		text("endpoint", "Endpoint", "", "optional host:port", "")
	return m.openForm(f)
}

func (m *Model) renameForm(i *store.Iface) tea.Cmd {
	p := m.currentPeer()
	if p == nil {
		return nil
	}
	pub := p.PublicKey()
	f := newForm("Rename peer", "", func(v formValues) (tea.Cmd, error) {
		if err := i.RenamePeer(pub, v["name"]); err != nil {
			return nil, err
		}
		m.setOK("renamed")
		return nil, nil
	})
	f.text("name", "Name", p.Name, "name", "")
	return m.openForm(f)
}

func (m *Model) exportForm(i *store.Iface, pub string) tea.Cmd {
	if !i.HasClientConfig(pub) {
		if _, err := i.ClientConfig(pub); err != nil {
			m.setErr(err)
			return nil
		}
	}
	def, _ := os.Getwd()
	f := newForm("Export client config", "", func(v formValues) (tea.Cmd, error) {
		path, err := i.ExportClient(pub, v["path"])
		if err != nil {
			return nil, err
		}
		if u := os.Getenv("SUDO_UID"); u != "" && os.Geteuid() == 0 {
			uid, _ := strconv.Atoi(u)
			gid, _ := strconv.Atoi(os.Getenv("SUDO_GID"))
			_ = os.Chown(path, uid, gid)
		}
		m.setOK("written " + path)
		return nil, nil
	})
	f.text("path", "Path", def, "directory or file", "a directory gets <network>-<client>.conf")
	return m.openForm(f)
}

func (m *Model) confirmDeleteIface(i *store.Iface) tea.Cmd {
	m.confirm = &confirmDialog{
		text: fmt.Sprintf("Delete %s %s?\n\nThis removes %s (and wgtui's metadata incl. stored client keys).\nThe interface is stopped first.", i.Kind(), sBold.Render(i.Name), i.Path),
		action: func() tea.Cmd {
			return m.op("deleting "+i.Name, func() error {
				if m.isUp(i) {
					if err := m.rt.Down(i.Name, i.Path); err != nil {
						return err
					}
				}
				if m.auto[i.Name] {
					_ = m.rt.SetAutostart(i.Name, false)
				}
				err := m.store.Delete(i)
				m.clampSel()
				return err
			}, i.Name+" deleted")
		},
	}
	return nil
}

func (m *Model) confirmDeletePeer(i *store.Iface, p *wgconf.Section) tea.Cmd {
	pub := p.PublicKey()
	m.confirm = &confirmDialog{
		text: fmt.Sprintf("Remove peer %s from %s?\n\nIts key is revoked; the device can no longer connect.", sBold.Render(store.PeerName(p)), i.Name),
		action: func() tea.Cmd {
			if err := i.RemovePeer(pub); err != nil {
				m.setErr(err)
				return nil
			}
			m.clampSel()
			return m.applyPeers(i, "peer removed")
		},
	}
	return nil
}

func (m *Model) showPeer() tea.Cmd {
	i, p := m.current(), m.currentPeer()
	if i == nil || p == nil {
		return nil
	}
	return m.showPeerCmd(i, p)
}

func (m *Model) showPeerCmd(i *store.Iface, p *wgconf.Section) tea.Cmd {
	pub := p.PublicKey()
	tv := textView{title: store.PeerName(p), iface: i, peer: pub}
	cfg, err := i.ClientConfig(pub)
	if err != nil {
		var b strings.Builder
		b.WriteString(p.String() + "\n")
		b.WriteString(sMuted.Render(err.Error()))
		tv.body = b.String()
		tv.peer = ""
	} else {
		tv.title = "Client config: " + store.PeerName(p)
		tv.body = cfg
		tv.qr = cfg
		tv.showQR = true
		tv.note = "scan with the WireGuard mobile app"
	}
	return func() tea.Msg { return showTextMsg(tv) }
}

func (m *Model) showIface(i *store.Iface) tea.Cmd {
	data, err := os.ReadFile(i.Path)
	body := string(data)
	if err != nil {
		body = err.Error()
	}
	body = redact(body)
	tv := textView{title: i.Path, body: body, note: "private keys hidden"}
	return func() tea.Msg { return showTextMsg(tv) }
}

func redact(conf string) string {
	lines := strings.Split(conf, "\n")
	for n, l := range lines {
		k, _, ok := strings.Cut(l, "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), "PrivateKey") {
			lines[n] = "PrivateKey = " + sMuted.Render("(hidden)")
		}
	}
	return strings.Join(lines, "\n")
}

// ---------- view ----------

// View renders the UI.
func (m *Model) View() string {
	if m.width == 0 {
		return "loading…"
	}
	base := m.mainView()
	var overlay string
	switch {
	case m.form != nil:
		overlay = m.form.view(m.width)
	case m.confirm != nil:
		overlay = sDialog.BorderForeground(cRed).Render(m.confirm.text + "\n\n" + sKey.Render("y") + sMuted.Render(" confirm · any other key cancels"))
	case m.text != nil:
		overlay = m.textViewRender()
	case m.help:
		overlay = m.helpView()
	}
	if overlay != "" {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlay,
			lipgloss.WithWhitespaceChars(" "))
	}
	return base
}

func (m *Model) headerView() string {
	title := sTitle.Render("wgtui")
	dir := m.store.Dir
	if m.demo {
		dir = "example data"
	}
	info := sMuted.Render(fmt.Sprintf("  %s · %d interface(s)", dir, len(m.store.Ifaces)))
	var right string
	switch {
	case m.demo:
		right = sPurple.Render("demo mode")
	case m.root:
		right = sGreen.Render("root ✓")
	default:
		right = sYellow.Render("not root: no live status, up/down will fail")
	}
	gap := m.width - lipgloss.Width(title) - lipgloss.Width(info) - lipgloss.Width(right) - 1
	if gap < 1 {
		gap = 1
	}
	return title + info + strings.Repeat(" ", gap) + right
}

func (m *Model) footerView() string {
	type kh struct{ k, h string }
	var keys []kh
	i := m.current()
	if m.focus == focusPeers {
		keys = []kh{{"enter", "config/QR"}, {"w", "export"}, {"e", "rename"}, {"d", "remove"}, {"esc", "back"}}
		if i != nil && i.IsNetwork() {
			keys = append([]kh{{"a", "add client"}}, keys...)
		}
	} else {
		keys = []kh{{"n", "new network"}, {"c", "connect"}, {"i", "import"}}
		if i != nil {
			keys = append(keys, kh{"u", "up/down"}, kh{"e", "edit"})
			if i.IsNetwork() {
				keys = append(keys, kh{"a", "add client"})
			}
			keys = append(keys, kh{"d", "delete"})
		}
	}
	keys = append(keys, kh{"?", "help"}, kh{"q", "quit"})
	var parts []string
	for _, k := range keys {
		parts = append(parts, sKey.Render(k.k)+" "+sMuted.Render(k.h))
	}
	line := strings.Join(parts, sMuted.Render(" · "))
	var status string
	switch {
	case m.busy != "":
		status = sYellow.Render("◌ " + m.busy + "…")
	case m.msg != "" && m.msgErr:
		status = sRed.Render("✗ " + m.msg)
	case m.msg != "":
		status = sGreen.Render("✓ ") + sText.Render(m.msg)
	}
	return lipgloss.NewStyle().MaxWidth(m.width).Render(line) + "\n" + lipgloss.NewStyle().MaxWidth(m.width).Render(status)
}

func (m *Model) mainView() string {
	header := m.headerView()
	footer := m.footerView()
	h := m.height - lipgloss.Height(header) - lipgloss.Height(footer)
	if h < 6 {
		h = 6
	}
	listW := 30
	if m.width < 90 {
		listW = 24
	}
	detailW := m.width - listW
	list := m.listView(listW-4, h-2)
	detail := m.detailView(detailW-4, h-2)
	ls, ds := sPane, sPane
	if m.focus == focusList {
		ls = sFocused
	} else {
		ds = sFocused
	}
	left := ls.Width(listW - 2).Height(h - 2).Render(list)
	right := ds.Width(detailW - 2).Height(h - 2).Render(detail)
	return header + "\n" + lipgloss.JoinHorizontal(lipgloss.Top, left, right) + "\n" + footer
}

func (m *Model) dot(i *store.Iface) string {
	if m.isUp(i) {
		return sGreen.Render("●")
	}
	return sMuted.Render("○")
}

func (m *Model) listView(w, h int) string {
	var b strings.Builder
	b.WriteString(sHeader.Render("Interfaces") + "\n\n")
	if len(m.store.Ifaces) == 0 {
		b.WriteString(sMuted.Render("nothing here yet\n\n") + sKey.Render("n") + sMuted.Render(" new network\n") +
			sKey.Render("c") + sMuted.Render(" connect to server\n") + sKey.Render("i") + sMuted.Render(" import .conf"))
		return b.String()
	}
	for _, kind := range []string{store.KindNetwork, store.KindConnection} {
		title := map[string]string{store.KindNetwork: "NETWORKS", store.KindConnection: "CONNECTIONS"}[kind]
		first := true
		for n, i := range m.store.Ifaces {
			if i.Meta.Kind != kind {
				continue
			}
			if first {
				b.WriteString(sMuted.Render(title) + "\n")
				first = false
			}
			sub := ""
			if kind == store.KindNetwork {
				sub = i.Meta.Mode
			} else if len(i.File.Peers) > 0 {
				sub = hostOf(i.File.Peers[0].Get("Endpoint"))
			}
			name := lipgloss.NewStyle().Width(10).Render(truncate(i.Name, 10))
			line := " " + name + " " + truncate(sub, w-15)
			if n == m.sel {
				st := sSel
				if m.focus != focusList {
					st = st.Background(lipgloss.Color("#24283B"))
				}
				b.WriteString(m.dot(i) + st.Width(w-1).Render(line) + "\n")
			} else {
				b.WriteString(m.dot(i) + sText.Render(" "+name) + " " + sMuted.Render(truncate(sub, w-15)) + "\n")
			}
		}
		if !first {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func hostOf(ep string) string {
	if i := strings.LastIndex(ep, ":"); i > 0 {
		return strings.Trim(ep[:i], "[]")
	}
	return ep
}

func truncate(s string, n int) string {
	if n <= 1 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func row(label, value string) string { return sLabel.Render(label) + value + "\n" }

func (m *Model) detailView(w, h int) string {
	i := m.current()
	if i == nil {
		return sHeader.Render("Welcome to wgtui") + "\n\n" + sText.Render(
			"Manage WireGuard networks and connections.\n\n") +
			sKey.Render("  n") + sText.Render("  create a network — this machine is the server\n") +
			sKey.Render("  c") + sText.Render("  connect to a WireGuard server\n") +
			sKey.Render("  i") + sText.Render("  import an existing .conf file\n\n") +
			sMuted.Render("Existing configs in "+m.store.Dir+" are picked up automatically.")
	}
	var b strings.Builder
	st := m.status[i.Name]
	up := m.isUp(i)
	b.WriteString(sHeader.Render(i.Name) + sMuted.Render(" · "+i.Kind()))
	if !i.HasMeta {
		b.WriteString(sYellow.Render("  (detected, not yet managed)"))
	}
	b.WriteString("\n\n")
	state := sMuted.Render("○ down")
	if up {
		state = sGreen.Render("● up")
	}
	auto := ""
	if m.store.Dir == systemDir || m.demo {
		auto = sMuted.Render("    autostart ") + map[bool]string{true: sGreen.Render("on"), false: sMuted.Render("off")}[m.auto[i.Name]]
	}
	b.WriteString(row("Status", state+auto))
	addrs := strings.Join(i.File.Interface.GetList("Address"), ", ")
	if i.IsNetwork() {
		b.WriteString(row("Address", sText.Render(addrs)+sMuted.Render("   port ")+sText.Render(strconv.Itoa(i.ListenPort()))))
		mode := i.Meta.Mode
		modeStr := sPurple.Render(mode) + sMuted.Render(" — "+store.ModeDescription(mode))
		b.WriteString(row("Visibility", modeStr))
		ft := sMuted.Render("split tunnel")
		if i.Meta.FullTunnel {
			ft = sText.Render("all client traffic via ") + sText.Render(i.Meta.OutIface) + sMuted.Render(" (NAT)")
		}
		b.WriteString(row("Routing", ft))
		ep := i.EndpointWithPort()
		if ep == "" {
			ep = sYellow.Render("not set — needed for client configs (e)")
		} else {
			ep = sText.Render(ep)
		}
		b.WriteString(row("Endpoint", ep))
	} else {
		b.WriteString(row("Address", sText.Render(addrs)))
		if dns := i.File.Interface.Get("DNS"); dns != "" {
			b.WriteString(row("DNS", sText.Render(dns)))
		}
	}
	b.WriteString(row("Public key", sAccent.Render(i.PublicKey())))
	b.WriteString("\n")

	peers := i.File.Peers
	if i.IsNetwork() {
		online := 0
		if st != nil {
			for _, p := range peers {
				if ps, ok := st.Peers[p.PublicKey()]; ok && !ps.LastHandshake.IsZero() && time.Since(ps.LastHandshake) < 3*time.Minute {
					online++
				}
			}
		}
		b.WriteString(sHeader.Render(fmt.Sprintf("Clients (%d)", len(peers))))
		if up && st != nil {
			b.WriteString(sMuted.Render(fmt.Sprintf("  %d online", online)))
		}
		b.WriteString("\n")
	} else {
		b.WriteString(sMuted.Render("give your public key to the server admin (or press v to view the config)") + "\n\n")
		b.WriteString(sHeader.Render("Server") + "\n")
	}
	if len(peers) == 0 {
		b.WriteString(sMuted.Render("\nno clients yet — press ") + sKey.Render("a") + sMuted.Render(" to add one"))
		return b.String()
	}
	// table
	nameW, ipW, hsW, trW := 14, 16, 11, 24
	epW := w - nameW - ipW - hsW - trW - 2
	showEP := epW >= 12
	if !showEP {
		epW = 0
	}
	hdr := "  " + pad("NAME", nameW) + pad("ALLOWED IPS", ipW)
	if showEP {
		hdr += pad("ENDPOINT", epW)
	}
	hdr += pad("HANDSHAKE", hsW) + "↓ RX / ↑ TX"
	b.WriteString(sMuted.Render(hdr) + "\n")

	used := lipgloss.Height(b.String())
	rows := h - used
	if rows < 1 {
		rows = 1
	}
	if m.peerSel < m.peerOff {
		m.peerOff = m.peerSel
	}
	if m.peerSel >= m.peerOff+rows {
		m.peerOff = m.peerSel - rows + 1
	}
	for n := m.peerOff; n < len(peers) && n < m.peerOff+rows; n++ {
		p := peers[n]
		var ps wg.PeerStatus
		have := false
		if st != nil {
			ps, have = st.Peers[p.PublicKey()]
		}
		hs := "—"
		hsStyle := sMuted
		if have && !ps.LastHandshake.IsZero() {
			d := time.Since(ps.LastHandshake)
			hs = ago(d)
			switch {
			case d < 3*time.Minute:
				hsStyle = sGreen
			case d < 15*time.Minute:
				hsStyle = sYellow
			}
		} else if up && st != nil {
			hs = "never"
		}
		tr := ""
		if have {
			tr = humanBytes(ps.Rx) + " / " + humanBytes(ps.Tx)
		}
		ep := p.Get("Endpoint")
		if have && ps.Endpoint != "" {
			ep = ps.Endpoint
		}
		name := store.PeerName(p)
		if p.Name == "" && !i.IsNetwork() && p.Get("Endpoint") != "" {
			name = hostOf(p.Get("Endpoint"))
		}
		if i.IsNetwork() && i.HasClientConfig(p.PublicKey()) {
			name = truncate(name, nameW-3)
		} else {
			name = truncate(name, nameW-1)
		}
		selected := m.focus == focusPeers && n == m.peerSel
		marker := "  "
		if selected {
			marker = sAccent.Render("▸ ")
		}
		line := pad(name, nameW) + pad(truncate(strings.Join(p.GetList("AllowedIPs"), ","), ipW-1), ipW)
		if showEP {
			line += pad(truncate(ep, epW-1), epW)
		}
		if selected {
			line = sSel.Render(line+pad(hs, hsW)) + sSel.Render(pad(tr, trW))
		} else {
			line = sText.Render(line) + hsStyle.Render(pad(hs, hsW)) + sMuted.Render(tr)
		}
		b.WriteString(marker + line + "\n")
	}
	if len(peers) > m.peerOff+rows {
		b.WriteString(sMuted.Render(fmt.Sprintf("  … %d more", len(peers)-m.peerOff-rows)))
	}
	return b.String()
}

func pad(s string, w int) string {
	n := lipgloss.Width(s)
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func (m *Model) textViewRender() string {
	t := m.text
	body := t.body
	if t.qr != "" && t.showQR {
		qr := renderQR(t.qr)
		if lipgloss.Width(body)+lipgloss.Width(qr)+12 <= m.width {
			body = lipgloss.JoinHorizontal(lipgloss.Top, body, "   ", qr)
		} else {
			body = qr
		}
	}
	lines := strings.Split(body, "\n")
	maxLines := m.height - 10
	if maxLines < 5 {
		maxLines = 5
	}
	if t.scroll > len(lines)-maxLines {
		t.scroll = max(0, len(lines)-maxLines)
	}
	// Never clip a QR code: a partial code does not scan.
	if len(lines) > maxLines && !(t.qr != "" && t.showQR) {
		lines = lines[t.scroll : t.scroll+maxLines]
	}
	var hints []string
	if t.qr != "" {
		hints = append(hints, sKey.Render("t")+sMuted.Render(" toggle QR"))
	}
	if t.iface != nil && t.peer != "" {
		hints = append(hints, sKey.Render("w")+sMuted.Render(" save to file"))
	}
	hints = append(hints, sKey.Render("↑↓")+sMuted.Render(" scroll"), sKey.Render("esc")+sMuted.Render(" close"))
	note := ""
	if t.note != "" {
		note = sMuted.Italic(true).Render(t.note) + "\n"
	}
	return sDialog.Render(sHeader.Render(t.title) + "\n\n" + strings.Join(lines, "\n") + "\n\n" + note + strings.Join(hints, sMuted.Render(" · ")))
}

func (m *Model) helpView() string {
	sec := func(t string) string { return "\n" + sHeader.Render(t) + "\n" }
	k := func(key, desc string) string { return "  " + sKey.Render(pad(key, 12)) + sText.Render(desc) + "\n" }
	var b strings.Builder
	b.WriteString(sHeader.Render("wgtui — keys") + "\n")
	b.WriteString(sec("Navigation"))
	b.WriteString(k("↑↓ / j k", "move"))
	b.WriteString(k("tab ←→ enter", "switch between interface list and peers"))
	b.WriteString(k("r", "reload configs from disk"))
	b.WriteString(sec("Interfaces"))
	b.WriteString(k("n", "new network (this machine is the server)"))
	b.WriteString(k("c", "connect to a server (new client connection)"))
	b.WriteString(k("i", "import an existing .conf file"))
	b.WriteString(k("u / space", "bring up / down (wg-quick)"))
	b.WriteString(k("b", "toggle autostart at boot (systemd)"))
	b.WriteString(k("e", "edit settings (visibility mode, routing, …)"))
	b.WriteString(k("v", "view config file"))
	b.WriteString(k("d", "delete"))
	b.WriteString(sec("Clients / peers"))
	b.WriteString(k("a", "add client (keys + config + QR generated)"))
	b.WriteString(k("p", "add peer by public key"))
	b.WriteString(k("enter / s", "show client config and QR code"))
	b.WriteString(k("w", "write client config to a file"))
	b.WriteString(k("e", "rename"))
	b.WriteString(k("d", "remove"))
	b.WriteString("\n" + sMuted.Render("press any key to close"))
	return sDialog.Render(b.String())
}

// Package store manages WireGuard interfaces in a configuration directory
// (normally /etc/wireguard). Every <name>.conf is an interface. wgtui keeps
// extra information it needs (network mode, public endpoint, client private
// keys) in a sidecar file <dir>/wgtui/<name>.json, so the .conf files stay
// plain wg-quick configs that keep working without wgtui.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/KsmBl/wgtui/internal/wg"
	"github.com/KsmBl/wgtui/internal/wgconf"
)

// Interface kinds.
const (
	KindNetwork    = "network"    // we are the hub, peers are our clients
	KindConnection = "connection" // we connect to a remote server
)

// Network modes.
const (
	ModeMesh     = "mesh"     // every client can reach every other client
	ModeIsolated = "isolated" // clients can only reach the server
	ModeCustom   = "custom"   // wgtui does not touch routing/firewall
)

// Modes lists the network modes in UI order.
var Modes = []string{ModeMesh, ModeIsolated, ModeCustom}

// ModeDescription explains a mode in one line.
func ModeDescription(m string) string {
	switch m {
	case ModeMesh:
		return "clients see each other (routed through the server)"
	case ModeIsolated:
		return "clients only see the server"
	default:
		return "wgtui leaves routing and firewall alone"
	}
}

var validName = regexp.MustCompile(`^[a-zA-Z0-9_=+.-]{1,15}$`)

// ValidName reports whether s is a valid interface name.
func ValidName(s string) bool { return validName.MatchString(s) }

// Meta is the wgtui sidecar data of an interface.
type Meta struct {
	Kind       string            `json:"kind"`
	Mode       string            `json:"mode,omitempty"`
	Endpoint   string            `json:"endpoint,omitempty"` // public host[:port] for clients
	ClientDNS  string            `json:"client_dns,omitempty"`
	FullTunnel bool              `json:"full_tunnel,omitempty"` // route all client traffic via server
	OutIface   string            `json:"out_iface,omitempty"`   // NAT uplink for full tunnel
	Keepalive  int               `json:"keepalive,omitempty"`
	ClientKeys map[string]string `json:"client_keys,omitempty"` // client public -> private key
	Created    time.Time         `json:"created,omitempty"`
}

// Iface is one WireGuard interface (one .conf file).
type Iface struct {
	Name    string
	Path    string
	File    *wgconf.File
	Meta    Meta
	HasMeta bool // false for configs wgtui has never saved
	store   *Store
}

// Store is the set of interfaces in a directory.
type Store struct {
	Dir    string
	Ifaces []*Iface
	Errors []string // files that could not be parsed
}

func (s *Store) metaDir() string             { return filepath.Join(s.Dir, "wgtui") }
func (s *Store) metaPath(name string) string { return filepath.Join(s.metaDir(), name+".json") }
func (s *Store) confPath(name string) string { return filepath.Join(s.Dir, name+".conf") }

// Load reads every *.conf file in dir.
func Load(dir string) (*Store, error) {
	s := &Store{Dir: dir}
	return s, s.Reload()
}

// Reload re-reads the directory.
func (s *Store) Reload() error {
	s.Ifaces = nil
	s.Errors = nil
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return err
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".conf") {
			paths = append(paths, filepath.Join(s.Dir, e.Name()))
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		name := strings.TrimSuffix(filepath.Base(p), ".conf")
		if !ValidName(name) {
			continue
		}
		f, err := wgconf.ParseFile(p)
		if err != nil {
			s.Errors = append(s.Errors, fmt.Sprintf("%s: %v", filepath.Base(p), err))
			continue
		}
		it := &Iface{Name: name, Path: p, File: f, store: s}
		it.loadMeta()
		s.Ifaces = append(s.Ifaces, it)
	}
	return nil
}

// Get returns the interface with that name.
func (s *Store) Get(name string) *Iface {
	for _, i := range s.Ifaces {
		if i.Name == name {
			return i
		}
	}
	return nil
}

func (i *Iface) loadMeta() {
	data, err := os.ReadFile(i.store.metaPath(i.Name))
	if err == nil && json.Unmarshal(data, &i.Meta) == nil {
		i.HasMeta = true
	} else {
		i.Meta = detectMeta(i.File)
	}
	if i.Meta.Kind == "" {
		i.Meta.Kind = detectMeta(i.File).Kind
	}
	if i.Meta.Kind == KindNetwork && i.Meta.Mode == "" {
		i.Meta.Mode = ModeCustom
	}
	if i.Meta.ClientKeys == nil {
		i.Meta.ClientKeys = map[string]string{}
	}
}

// detectMeta guesses what an existing, foreign config is.
func detectMeta(f *wgconf.File) Meta {
	m := Meta{Kind: KindNetwork, Mode: ModeCustom}
	withEndpoint := 0
	for _, p := range f.Peers {
		if p.Get("Endpoint") != "" {
			withEndpoint++
		}
	}
	listen := f.Interface.Get("ListenPort") != ""
	if withEndpoint > 0 && (!listen || len(f.Peers) == 1) {
		m.Kind = KindConnection
		m.Mode = ""
		return m
	}
	// A wgtui-generated firewall block whose sidecar got lost.
	for _, kv := range f.Managed {
		switch {
		case strings.Contains(kv.Value, "-o %i -j DROP"):
			m.Mode = ModeIsolated
		case strings.Contains(kv.Value, "-o %i -j ACCEPT"):
			m.Mode = ModeMesh
		case strings.Contains(kv.Value, "MASQUERADE"):
			m.FullTunnel = true
			if f := strings.Fields(kv.Value); len(f) > 0 {
				for j, w := range f {
					if w == "-o" && j+1 < len(f) {
						m.OutIface = strings.TrimSuffix(f[j+1], ";")
					}
				}
			}
		}
	}
	return m
}

// IsNetwork reports whether this interface is a hub we manage.
func (i *Iface) IsNetwork() bool { return i.Meta.Kind == KindNetwork }

// Address returns the first IPv4 interface address, e.g. 10.8.0.1/24.
func (i *Iface) Address() (netip.Prefix, bool) {
	for _, a := range i.File.Interface.GetList("Address") {
		if p, err := netip.ParsePrefix(a); err == nil && p.Addr().Is4() {
			return p, true
		}
		if ad, err := netip.ParseAddr(a); err == nil && ad.Is4() {
			return netip.PrefixFrom(ad, 32), true
		}
	}
	return netip.Prefix{}, false
}

// Subnet returns the network prefix of the interface address.
func (i *Iface) Subnet() (netip.Prefix, bool) {
	a, ok := i.Address()
	if !ok {
		return netip.Prefix{}, false
	}
	return a.Masked(), true
}

// ListenPort of the interface (0 if unset).
func (i *Iface) ListenPort() int {
	p, _ := strconv.Atoi(i.File.Interface.Get("ListenPort"))
	return p
}

// PublicKey of the interface.
func (i *Iface) PublicKey() string {
	pub, _ := wg.PublicKey(i.File.Interface.Get("PrivateKey"))
	return pub
}

// PeerName returns a display name for a peer.
func PeerName(p *wgconf.Section) string {
	if p.Name != "" {
		return p.Name
	}
	if k := p.PublicKey(); len(k) > 8 {
		return k[:8] + "…"
	}
	return "(unnamed)"
}

// managedRules generates the PostUp/PostDown firewall lines for a network.
func (i *Iface) managedRules() []wgconf.KV {
	if !i.IsNetwork() {
		return nil
	}
	var up []string
	needForward := false
	if i.Meta.FullTunnel && i.Meta.OutIface != "" {
		if sub, ok := i.Subnet(); ok {
			up = append(up,
				"iptables -t nat %s POSTROUTING -s "+sub.String()+" -o "+i.Meta.OutIface+" -j MASQUERADE",
				"iptables %s FORWARD -o %%i -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT",
				"iptables %s FORWARD -i %%i -j ACCEPT",
			)
			needForward = true
		}
	}
	switch i.Meta.Mode {
	case ModeMesh:
		up = append(up, "iptables %s FORWARD -i %%i -o %%i -j ACCEPT")
		needForward = true
	case ModeIsolated:
		up = append(up, "iptables %s FORWARD -i %%i -o %%i -j DROP")
	}
	if len(up) == 0 {
		return nil
	}
	var out []wgconf.KV
	if needForward {
		out = append(out, wgconf.KV{Key: "PostUp", Value: "sysctl -q -w net.ipv4.ip_forward=1"})
	}
	// -I inserts at the top: the last rule inserted (the mode rule) ends
	// up first, so isolation wins over the generic forward accept.
	for _, r := range up {
		out = append(out, wgconf.KV{Key: "PostUp", Value: fmt.Sprintf(r, "-I")})
	}
	for _, r := range up {
		out = append(out, wgconf.KV{Key: "PostDown", Value: fmt.Sprintf(r, "-D")})
	}
	return out
}

// Save writes the .conf and the sidecar metadata.
func (i *Iface) Save() error {
	if i.Meta.Created.IsZero() {
		i.Meta.Created = time.Now().UTC().Truncate(time.Second)
	}
	// Forget keys of clients that no longer exist.
	for pub := range i.Meta.ClientKeys {
		if i.File.Peer(pub) == nil {
			delete(i.Meta.ClientKeys, pub)
		}
	}
	if i.HasMeta || i.IsNetwork() {
		i.File.Managed = i.managedRules()
	}
	if err := i.File.WriteFile(i.Path); err != nil {
		return err
	}
	if err := os.MkdirAll(i.store.metaDir(), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(i.Meta, "", "  ")
	if err != nil {
		return err
	}
	tmp := i.store.metaPath(i.Name) + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, i.store.metaPath(i.Name)); err != nil {
		return err
	}
	i.HasMeta = true
	return nil
}

// Delete removes the interface config and metadata.
func (s *Store) Delete(i *Iface) error {
	if err := os.Remove(i.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_ = os.Remove(s.metaPath(i.Name))
	for n, x := range s.Ifaces {
		if x == i {
			s.Ifaces = append(s.Ifaces[:n], s.Ifaces[n+1:]...)
			break
		}
	}
	return nil
}

func (s *Store) checkNew(name string) error {
	if !ValidName(name) {
		return fmt.Errorf("invalid interface name %q (max 15 chars, a-z 0-9 _=+.-)", name)
	}
	if _, err := os.Stat(s.confPath(name)); err == nil {
		return fmt.Errorf("%s.conf already exists", name)
	}
	return nil
}

// NetworkOpts are the settings of a network (hub).
type NetworkOpts struct {
	Name       string
	Address    string // server address with prefix, e.g. 10.8.0.1/24
	ListenPort int
	Endpoint   string
	DNS        string
	Mode       string
	FullTunnel bool
	OutIface   string
	Keepalive  int
}

func (o *NetworkOpts) validate() error {
	p, err := netip.ParsePrefix(o.Address)
	if err != nil || !p.Addr().Is4() {
		return fmt.Errorf("address must be IPv4 with prefix, e.g. 10.8.0.1/24")
	}
	if p.Bits() > 30 {
		return fmt.Errorf("prefix /%d is too small for a network", p.Bits())
	}
	if p.Addr() == p.Masked().Addr() {
		return fmt.Errorf("address is the network address; use e.g. %s", p.Addr().Next())
	}
	if o.ListenPort < 1 || o.ListenPort > 65535 {
		return fmt.Errorf("listen port must be 1-65535")
	}
	if o.FullTunnel && o.OutIface == "" {
		return fmt.Errorf("full tunnel needs an outgoing interface for NAT")
	}
	for _, d := range splitList(o.DNS) {
		if _, err := netip.ParseAddr(d); err != nil {
			return fmt.Errorf("invalid DNS server %q", d)
		}
	}
	return nil
}

// CreateNetwork creates a new hub interface.
func (s *Store) CreateNetwork(o NetworkOpts) (*Iface, error) {
	if err := s.checkNew(o.Name); err != nil {
		return nil, err
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	priv, _, err := wg.GenerateKey()
	if err != nil {
		return nil, err
	}
	f := &wgconf.File{Interface: &wgconf.Section{Kind: "Interface"}}
	f.Interface.Set("PrivateKey", priv)
	i := &Iface{Name: o.Name, Path: s.confPath(o.Name), File: f, store: s,
		Meta: Meta{Kind: KindNetwork, ClientKeys: map[string]string{}}}
	i.applyNetworkOpts(o)
	if err := i.Save(); err != nil {
		return nil, err
	}
	s.Ifaces = append(s.Ifaces, i)
	return i, nil
}

func (i *Iface) applyNetworkOpts(o NetworkOpts) {
	i.File.Interface.Set("Address", o.Address)
	i.File.Interface.Set("ListenPort", strconv.Itoa(o.ListenPort))
	i.Meta.Mode = o.Mode
	i.Meta.Endpoint = o.Endpoint
	i.Meta.ClientDNS = o.DNS
	i.Meta.FullTunnel = o.FullTunnel
	i.Meta.OutIface = o.OutIface
	i.Meta.Keepalive = o.Keepalive
}

// NetworkOpts returns the current settings of a network.
func (i *Iface) NetworkOpts() NetworkOpts {
	o := NetworkOpts{
		Name: i.Name, ListenPort: i.ListenPort(), Endpoint: i.Meta.Endpoint,
		DNS: i.Meta.ClientDNS, Mode: i.Meta.Mode, FullTunnel: i.Meta.FullTunnel,
		OutIface: i.Meta.OutIface, Keepalive: i.Meta.Keepalive,
	}
	if a, ok := i.Address(); ok {
		o.Address = a.String()
	}
	return o
}

// UpdateNetwork changes the settings of a network.
func (i *Iface) UpdateNetwork(o NetworkOpts) error {
	if err := o.validate(); err != nil {
		return err
	}
	// Keep additional (e.g. IPv6) addresses.
	addrs := []string{o.Address}
	old, _ := i.Address()
	for _, a := range i.File.Interface.GetList("Address") {
		if a != old.String() {
			addrs = append(addrs, a)
		}
	}
	i.applyNetworkOpts(o)
	i.File.Interface.Set("Address", strings.Join(addrs, ", "))
	return i.Save()
}

// NextIP returns the next free host address of a network.
func (i *Iface) NextIP() (netip.Addr, error) {
	sub, ok := i.Subnet()
	if !ok {
		return netip.Addr{}, fmt.Errorf("interface has no IPv4 address")
	}
	used := map[netip.Addr]bool{}
	if a, ok := i.Address(); ok {
		used[a.Addr()] = true
	}
	for _, p := range i.File.Peers {
		for _, a := range p.GetList("AllowedIPs") {
			if pr, err := netip.ParsePrefix(a); err == nil && pr.Bits() == 32 {
				used[pr.Addr()] = true
			}
		}
	}
	last := lastAddr(sub)
	for a := sub.Addr().Next(); sub.Contains(a) && a != last; a = a.Next() {
		if !used[a] {
			return a, nil
		}
	}
	return netip.Addr{}, fmt.Errorf("no free address left in %s", sub)
}

func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Masked().Addr().As4()
	host := 32 - p.Bits()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	v |= (1 << host) - 1
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// AddClient adds a new client to a network and returns its peer section.
func (i *Iface) AddClient(name string, ip string) (*wgconf.Section, error) {
	if !i.IsNetwork() {
		return nil, fmt.Errorf("%s is not a network", i.Name)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	for _, p := range i.File.Peers {
		if strings.EqualFold(p.Name, name) {
			return nil, fmt.Errorf("a client named %q already exists", name)
		}
	}
	var addr netip.Addr
	if ip == "" {
		a, err := i.NextIP()
		if err != nil {
			return nil, err
		}
		addr = a
	} else {
		a, err := netip.ParseAddr(strings.TrimSuffix(ip, "/32"))
		if err != nil || !a.Is4() {
			return nil, fmt.Errorf("invalid IPv4 address %q", ip)
		}
		if sub, ok := i.Subnet(); ok && !sub.Contains(a) {
			return nil, fmt.Errorf("%s is outside of %s", a, sub)
		}
		addr = a
	}
	priv, pub, err := wg.GenerateKey()
	if err != nil {
		return nil, err
	}
	psk, err := wg.GeneratePSK()
	if err != nil {
		return nil, err
	}
	p := &wgconf.Section{Kind: "Peer", Name: name}
	p.Set("PublicKey", pub)
	p.Set("PresharedKey", psk)
	p.Set("AllowedIPs", netip.PrefixFrom(addr, 32).String())
	i.File.Peers = append(i.File.Peers, p)
	i.Meta.ClientKeys[pub] = priv
	if err := i.Save(); err != nil {
		i.File.RemovePeer(pub)
		return nil, err
	}
	return p, nil
}

// AddPeer adds a peer whose private key we do not know (e.g. a device
// that generated its own keys).
func (i *Iface) AddPeer(name, pub, allowed, endpoint string) (*wgconf.Section, error) {
	if !wg.ValidKey(pub) {
		return nil, fmt.Errorf("invalid public key")
	}
	if i.File.Peer(pub) != nil {
		return nil, fmt.Errorf("peer with this key already exists")
	}
	if allowed == "" && i.IsNetwork() {
		a, err := i.NextIP()
		if err != nil {
			return nil, err
		}
		allowed = netip.PrefixFrom(a, 32).String()
	}
	for _, a := range splitList(allowed) {
		if _, err := netip.ParsePrefix(a); err != nil {
			return nil, fmt.Errorf("invalid allowed IP %q", a)
		}
	}
	p := &wgconf.Section{Kind: "Peer", Name: strings.TrimSpace(name)}
	p.Set("PublicKey", pub)
	p.Set("AllowedIPs", allowed)
	p.Set("Endpoint", endpoint)
	i.File.Peers = append(i.File.Peers, p)
	if err := i.Save(); err != nil {
		i.File.RemovePeer(pub)
		return nil, err
	}
	return p, nil
}

// RemovePeer deletes a peer.
func (i *Iface) RemovePeer(pub string) error {
	if !i.File.RemovePeer(pub) {
		return fmt.Errorf("peer not found")
	}
	delete(i.Meta.ClientKeys, pub)
	return i.Save()
}

// RenamePeer changes the friendly name of a peer.
func (i *Iface) RenamePeer(pub, name string) error {
	p := i.File.Peer(pub)
	if p == nil {
		return fmt.Errorf("peer not found")
	}
	p.Name = strings.TrimSpace(name)
	return i.Save()
}

// HasClientConfig reports whether wgtui can render a config for that peer.
func (i *Iface) HasClientConfig(pub string) bool {
	return i.IsNetwork() && i.Meta.ClientKeys[pub] != ""
}

// ClientAllowedIPs is what a client of this network routes into the tunnel.
func (i *Iface) ClientAllowedIPs() string {
	if i.Meta.FullTunnel {
		return "0.0.0.0/0"
	}
	switch i.Meta.Mode {
	case ModeIsolated:
		if a, ok := i.Address(); ok {
			return netip.PrefixFrom(a.Addr(), 32).String()
		}
	}
	if s, ok := i.Subnet(); ok {
		return s.String()
	}
	return ""
}

// EndpointWithPort returns the public endpoint including the port.
func (i *Iface) EndpointWithPort() string {
	ep := strings.TrimSpace(i.Meta.Endpoint)
	if ep == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(ep); err == nil {
		return ep
	}
	return net.JoinHostPort(strings.Trim(ep, "[]"), strconv.Itoa(i.ListenPort()))
}

// ClientConfig renders the wg-quick config for a client of this network.
func (i *Iface) ClientConfig(pub string) (string, error) {
	p := i.File.Peer(pub)
	if p == nil {
		return "", fmt.Errorf("peer not found")
	}
	priv := i.Meta.ClientKeys[pub]
	if priv == "" {
		return "", fmt.Errorf("private key of %s is unknown (peer was not created by wgtui)", PeerName(p))
	}
	ep := i.EndpointWithPort()
	if ep == "" {
		return "", fmt.Errorf("set the public endpoint of %s first (e (edit) on the network)", i.Name)
	}
	c := &wgconf.File{Interface: &wgconf.Section{Kind: "Interface", Name: p.Name}}
	c.Interface.Set("PrivateKey", priv)
	c.Interface.Set("Address", p.Get("AllowedIPs"))
	c.Interface.Set("DNS", i.Meta.ClientDNS)
	sp := &wgconf.Section{Kind: "Peer", Name: i.Name}
	sp.Set("PublicKey", i.PublicKey())
	sp.Set("PresharedKey", p.Get("PresharedKey"))
	sp.Set("Endpoint", ep)
	sp.Set("AllowedIPs", i.ClientAllowedIPs())
	if i.Meta.Keepalive > 0 {
		sp.Set("PersistentKeepalive", strconv.Itoa(i.Meta.Keepalive))
	}
	c.Peers = []*wgconf.Section{sp}
	return c.String(), nil
}

// ConnectionOpts describe a connection to a remote server.
type ConnectionOpts struct {
	Name          string
	Address       string
	PrivateKey    string // empty: generate
	DNS           string
	PeerName      string
	PeerPublicKey string
	PresharedKey  string
	Endpoint      string
	AllowedIPs    string
	Keepalive     int
}

func (o *ConnectionOpts) validate() error {
	for _, a := range splitList(o.Address) {
		if _, err := netip.ParsePrefix(a); err != nil {
			if _, err := netip.ParseAddr(a); err != nil {
				return fmt.Errorf("invalid address %q", a)
			}
		}
	}
	if len(splitList(o.Address)) == 0 {
		return fmt.Errorf("address is required (the IP the server assigned to you)")
	}
	if o.PrivateKey != "" && !wg.ValidKey(o.PrivateKey) {
		return fmt.Errorf("invalid private key")
	}
	if !wg.ValidKey(o.PeerPublicKey) {
		return fmt.Errorf("invalid server public key")
	}
	if o.PresharedKey != "" && !wg.ValidKey(o.PresharedKey) {
		return fmt.Errorf("invalid preshared key")
	}
	if _, _, err := net.SplitHostPort(o.Endpoint); err != nil {
		return fmt.Errorf("endpoint must be host:port")
	}
	if len(splitList(o.AllowedIPs)) == 0 {
		return fmt.Errorf("allowed IPs are required (0.0.0.0/0 for everything)")
	}
	for _, a := range splitList(o.AllowedIPs) {
		if _, err := netip.ParsePrefix(a); err != nil {
			return fmt.Errorf("invalid allowed IP %q", a)
		}
	}
	for _, d := range splitList(o.DNS) {
		if _, err := netip.ParseAddr(d); err != nil {
			return fmt.Errorf("invalid DNS server %q", d)
		}
	}
	return nil
}

// CreateConnection creates a client interface connecting to a server.
func (s *Store) CreateConnection(o ConnectionOpts) (*Iface, error) {
	if err := s.checkNew(o.Name); err != nil {
		return nil, err
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	if o.PrivateKey == "" {
		priv, _, err := wg.GenerateKey()
		if err != nil {
			return nil, err
		}
		o.PrivateKey = priv
	}
	f := &wgconf.File{Interface: &wgconf.Section{Kind: "Interface"}}
	i := &Iface{Name: o.Name, Path: s.confPath(o.Name), File: f, store: s,
		Meta: Meta{Kind: KindConnection, ClientKeys: map[string]string{}}}
	i.applyConnectionOpts(o)
	if err := i.Save(); err != nil {
		return nil, err
	}
	s.Ifaces = append(s.Ifaces, i)
	return i, nil
}

func (i *Iface) applyConnectionOpts(o ConnectionOpts) {
	in := i.File.Interface
	in.Set("PrivateKey", strings.TrimSpace(o.PrivateKey))
	in.Set("Address", strings.Join(splitList(o.Address), ", "))
	in.Set("DNS", strings.Join(splitList(o.DNS), ", "))
	var p *wgconf.Section
	if len(i.File.Peers) > 0 {
		p = i.File.Peers[0]
	} else {
		p = &wgconf.Section{Kind: "Peer"}
		i.File.Peers = append(i.File.Peers, p)
	}
	p.Name = o.PeerName
	p.Set("PublicKey", strings.TrimSpace(o.PeerPublicKey))
	p.Set("PresharedKey", strings.TrimSpace(o.PresharedKey))
	p.Set("Endpoint", strings.TrimSpace(o.Endpoint))
	p.Set("AllowedIPs", strings.Join(splitList(o.AllowedIPs), ", "))
	ka := ""
	if o.Keepalive > 0 {
		ka = strconv.Itoa(o.Keepalive)
	}
	p.Set("PersistentKeepalive", ka)
}

// ConnectionOpts returns the current settings of a connection.
func (i *Iface) ConnectionOpts() ConnectionOpts {
	in := i.File.Interface
	o := ConnectionOpts{
		Name: i.Name, Address: strings.Join(in.GetList("Address"), ", "),
		PrivateKey: in.Get("PrivateKey"), DNS: strings.Join(in.GetList("DNS"), ", "),
	}
	if len(i.File.Peers) > 0 {
		p := i.File.Peers[0]
		o.PeerName = p.Name
		o.PeerPublicKey = p.PublicKey()
		o.PresharedKey = p.Get("PresharedKey")
		o.Endpoint = p.Get("Endpoint")
		o.AllowedIPs = strings.Join(p.GetList("AllowedIPs"), ", ")
		o.Keepalive, _ = strconv.Atoi(p.Get("PersistentKeepalive"))
	}
	return o
}

// UpdateConnection changes a connection.
func (i *Iface) UpdateConnection(o ConnectionOpts) error {
	if err := o.validate(); err != nil {
		return err
	}
	i.applyConnectionOpts(o)
	return i.Save()
}

// Import copies an existing wg-quick config file into the store.
func (s *Store) Import(src, name string) (*Iface, error) {
	src = expandHome(src)
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	}
	if err := s.checkNew(name); err != nil {
		return nil, err
	}
	f, err := wgconf.ParseFile(src)
	if err != nil {
		return nil, err
	}
	if !wg.ValidKey(f.Interface.Get("PrivateKey")) {
		return nil, fmt.Errorf("config has no valid PrivateKey")
	}
	i := &Iface{Name: name, Path: s.confPath(name), File: f, store: s}
	i.Meta = detectMeta(f)
	i.Meta.ClientKeys = map[string]string{}
	if err := i.Save(); err != nil {
		return nil, err
	}
	s.Ifaces = append(s.Ifaces, i)
	return i, nil
}

// ExportClient writes a client config to path.
func (i *Iface) ExportClient(pub, path string) (string, error) {
	cfg, err := i.ClientConfig(pub)
	if err != nil {
		return "", err
	}
	path = expandHome(path)
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		name := PeerName(i.File.Peer(pub))
		path = filepath.Join(path, i.Name+"-"+sanitize(name)+".conf")
	}
	return path, os.WriteFile(path, []byte(cfg), 0o600)
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == ' ' || r == os.PathSeparator {
			return '_'
		}
		return r
	}, s)
}

func expandHome(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "~/") {
		home := os.Getenv("HOME")
		if u := os.Getenv("SUDO_USER"); u != "" && os.Geteuid() == 0 {
			home = "/home/" + u
		}
		return filepath.Join(home, p[2:])
	}
	return p
}

func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Kind returns "network" or "connection".
func (i *Iface) Kind() string { return i.Meta.Kind }

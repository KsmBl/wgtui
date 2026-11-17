package ui

import (
	"os"

	"github.com/KsmBl/wgtui/internal/store"
	"github.com/KsmBl/wgtui/internal/wg"
)

// DemoSetup creates a temporary config directory with example networks.
func DemoSetup() (*store.Store, *Demo, error) {
	dir, err := os.MkdirTemp("", "wgtui-demo-")
	if err != nil {
		return nil, nil, err
	}
	s, err := store.Load(dir)
	if err != nil {
		return nil, nil, err
	}
	home, err := s.CreateNetwork(store.NetworkOpts{Name: "wg0", Address: "10.8.0.1/24", ListenPort: 51820,
		Endpoint: "vpn.example.com", Mode: store.ModeMesh, DNS: "10.8.0.1", Keepalive: 25})
	if err != nil {
		return nil, nil, err
	}
	for _, n := range []string{"laptop", "phone", "nas", "tablet", "desktop"} {
		if _, err := home.AddClient(n, ""); err != nil {
			return nil, nil, err
		}
	}
	guests, err := s.CreateNetwork(store.NetworkOpts{Name: "guests", Address: "10.9.0.1/24", ListenPort: 51821,
		Endpoint: "vpn.example.com", Mode: store.ModeIsolated, FullTunnel: true, OutIface: "eth0", DNS: "1.1.1.1", Keepalive: 25})
	if err != nil {
		return nil, nil, err
	}
	for _, n := range []string{"guest-anna", "guest-ben"} {
		guests.AddClient(n, "")
	}
	_, pub, _ := wg.GenerateKey()
	if _, err := s.CreateConnection(store.ConnectionOpts{Name: "office", Address: "10.20.0.14/32", PeerName: "office-gw",
		PeerPublicKey: pub, Endpoint: "office.example.org:51820", AllowedIPs: "10.20.0.0/16, 192.168.50.0/24", Keepalive: 25}); err != nil {
		return nil, nil, err
	}
	_, pub2, _ := wg.GenerateKey()
	s.CreateConnection(store.ConnectionOpts{Name: "vps", Address: "10.66.66.2/32", PeerPublicKey: pub2,
		Endpoint: "198.51.100.23:51820", AllowedIPs: "0.0.0.0/0", DNS: "10.66.66.1", Keepalive: 25})
	return s, NewDemo(s, "wg0", "guests", "office"), nil
}

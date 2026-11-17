package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KsmBl/wgtui/internal/wg"
)

func TestNetworkLifecycle(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.CreateNetwork(NetworkOpts{Name: "wg0", Address: "10.8.0.1/24", ListenPort: 51820,
		Endpoint: "vpn.example.com", Mode: ModeIsolated, Keepalive: 25})
	if err != nil {
		t.Fatal(err)
	}
	a, err := n.AddClient("alice", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := n.AddClient("bob", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Get("AllowedIPs") != "10.8.0.2/32" || b.Get("AllowedIPs") != "10.8.0.3/32" {
		t.Fatalf("ips %s %s", a.Get("AllowedIPs"), b.Get("AllowedIPs"))
	}
	cfg, err := n.ClientConfig(a.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Endpoint = vpn.example.com:51820", "AllowedIPs = 10.8.0.1/32", "Address = 10.8.0.2/32", "PersistentKeepalive = 25"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("client config misses %q:\n%s", want, cfg)
		}
	}
	conf, _ := os.ReadFile(filepath.Join(dir, "wg0.conf"))
	if !strings.Contains(string(conf), "-o %i -j DROP") {
		t.Errorf("isolated rule missing:\n%s", conf)
	}

	o := n.NetworkOpts()
	o.Mode = ModeMesh
	o.FullTunnel = true
	o.OutIface = "eth0"
	if err := n.UpdateNetwork(o); err != nil {
		t.Fatal(err)
	}
	cfg, _ = n.ClientConfig(b.PublicKey())
	if !strings.Contains(cfg, "AllowedIPs = 0.0.0.0/0") {
		t.Errorf("full tunnel:\n%s", cfg)
	}

	// Reload from disk: everything must survive, rules must not duplicate.
	s2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	n2 := s2.Get("wg0")
	if n2 == nil || !n2.HasMeta || n2.Meta.Mode != ModeMesh || len(n2.File.Peers) != 2 {
		t.Fatalf("reload: %+v", n2)
	}
	if err := n2.Save(); err != nil {
		t.Fatal(err)
	}
	conf2, _ := os.ReadFile(filepath.Join(dir, "wg0.conf"))
	if strings.Count(string(conf2), "MASQUERADE") != 2 || !strings.Contains(string(conf2), "-o %i -j ACCEPT") {
		t.Errorf("rules after resave:\n%s", conf2)
	}
	if err := n2.RemovePeer(a.PublicKey()); err != nil {
		t.Fatal(err)
	}
	if _, ok := n2.Meta.ClientKeys[a.PublicKey()]; ok {
		t.Error("private key of removed client kept")
	}
	c, _ := n2.AddClient("carol", "")
	if c.Get("AllowedIPs") != "10.8.0.2/32" {
		t.Errorf("freed IP not reused: %s", c.Get("AllowedIPs"))
	}
}

func TestImportExisting(t *testing.T) {
	dir := t.TempDir()
	priv, _, _ := wg.GenerateKey()
	_, peer, _ := wg.GenerateKey()
	server := "[Interface]\nPrivateKey = " + priv + "\nAddress = 10.9.0.1/24\nListenPort = 51820\nPostUp = echo up\n\n### Client phone\n[Peer]\nPublicKey = " + peer + "\nAllowedIPs = 10.9.0.2/32\n"
	client := "[Interface]\nPrivateKey = " + priv + "\nAddress = 10.1.0.5/32\n\n[Peer]\nPublicKey = " + peer + "\nEndpoint = 1.2.3.4:51820\nAllowedIPs = 0.0.0.0/0\n"
	os.WriteFile(filepath.Join(dir, "srv.conf"), []byte(server), 0o600)
	os.WriteFile(filepath.Join(dir, "home.conf"), []byte(client), 0o600)
	os.WriteFile(filepath.Join(dir, "broken.conf"), []byte("garbage"), 0o600)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Ifaces) != 2 || len(s.Errors) != 1 {
		t.Fatalf("ifaces %d errors %v", len(s.Ifaces), s.Errors)
	}
	if s.Get("srv").Meta.Kind != KindNetwork || s.Get("srv").Meta.Mode != ModeCustom {
		t.Error("srv should be a custom network")
	}
	if s.Get("home").Meta.Kind != KindConnection {
		t.Error("home should be a connection")
	}
	if s.Get("srv").File.Peers[0].Name != "phone" {
		t.Error("peer name not imported")
	}
	// Saving an imported custom network must not change its rules.
	srv := s.Get("srv")
	srv.AddClient("new", "")
	data, _ := os.ReadFile(filepath.Join(dir, "srv.conf"))
	if !strings.Contains(string(data), "PostUp = echo up") || strings.Contains(string(data), "iptables") {
		t.Errorf("custom rules touched:\n%s", data)
	}
	if ip, _ := srv.NextIP(); ip.String() != "10.9.0.4" {
		t.Errorf("next ip %s", ip)
	}
}

func TestConnection(t *testing.T) {
	s, _ := Load(t.TempDir())
	_, pub, _ := wg.GenerateKey()
	c, err := s.CreateConnection(ConnectionOpts{Name: "office", Address: "10.0.0.7/32", PeerPublicKey: pub,
		Endpoint: "office.example.org:51820", AllowedIPs: "10.0.0.0/24, 192.168.10.0/24", Keepalive: 25})
	if err != nil {
		t.Fatal(err)
	}
	if !wg.ValidKey(c.File.Interface.Get("PrivateKey")) || c.PublicKey() == "" {
		t.Fatal("key not generated")
	}
	if _, err := s.CreateConnection(ConnectionOpts{Name: "bad", Address: "10.0.0.7/32", PeerPublicKey: pub, Endpoint: "nohost", AllowedIPs: "0.0.0.0/0"}); err == nil {
		t.Error("expected endpoint error")
	}
}

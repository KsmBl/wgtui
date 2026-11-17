// Package wg wraps key generation and the wg / wg-quick / systemctl tools.
package wg

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/curve25519"
)

// GenerateKey returns a new base64 private key and its public key.
func GenerateKey() (priv, pub string, err error) {
	var k [32]byte
	if _, err = rand.Read(k[:]); err != nil {
		return
	}
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	priv = base64.StdEncoding.EncodeToString(k[:])
	pub, err = PublicKey(priv)
	return
}

// GeneratePSK returns a random preshared key.
func GeneratePSK() (string, error) {
	var k [32]byte
	if _, err := rand.Read(k[:]); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(k[:]), nil
}

// PublicKey derives the public key from a base64 private key.
func PublicKey(priv string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(priv))
	if err != nil || len(raw) != 32 {
		return "", fmt.Errorf("invalid private key")
	}
	pub, err := curve25519.X25519(raw, curve25519.Basepoint)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(pub), nil
}

// ValidKey reports whether s is a base64 encoded 32 byte key.
func ValidKey(s string) bool {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	return err == nil && len(raw) == 32
}

func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	s := strings.TrimSpace(out.String())
	if err != nil {
		if s == "" {
			s = err.Error()
		}
		return s, fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), lastLine(s))
	}
	return s, nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// Up brings an interface up with wg-quick using the config path.
func Up(path string) error { _, err := run("wg-quick", "up", path); return err }

// Down tears an interface down with wg-quick.
func Down(path string) error { _, err := run("wg-quick", "down", path); return err }

// IsUp reports whether a network interface with that name exists.
func IsUp(name string) bool {
	_, err := net.InterfaceByName(name)
	return err == nil
}

// SyncConf applies peer changes to a running interface without
// disrupting existing sessions.
func SyncConf(name, path string) error {
	stripped, err := run("wg-quick", "strip", path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "wgtui-*.conf")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	if _, err := tmp.WriteString(stripped + "\n"); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	_, err = run("wg", "syncconf", name, tmp.Name())
	return err
}

// PeerStatus is runtime information about a peer.
type PeerStatus struct {
	Endpoint      string
	LastHandshake time.Time
	Rx, Tx        int64
}

// IfaceStatus is runtime information about an interface.
type IfaceStatus struct {
	ListenPort int
	Peers      map[string]PeerStatus
}

// Dump returns `wg show all dump` parsed by interface name.
func Dump() (map[string]*IfaceStatus, error) {
	out, err := run("wg", "show", "all", "dump")
	if err != nil {
		return nil, err
	}
	return ParseDump(out), nil
}

// ParseDump parses the output of `wg show all dump`.
func ParseDump(out string) map[string]*IfaceStatus {
	res := map[string]*IfaceStatus{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		switch len(f) {
		case 5: // iface priv pub port fwmark
			port, _ := strconv.Atoi(f[3])
			res[f[0]] = &IfaceStatus{ListenPort: port, Peers: map[string]PeerStatus{}}
		case 9: // iface pub psk endpoint allowed handshake rx tx keepalive
			st := res[f[0]]
			if st == nil {
				st = &IfaceStatus{Peers: map[string]PeerStatus{}}
				res[f[0]] = st
			}
			hs, _ := strconv.ParseInt(f[5], 10, 64)
			rx, _ := strconv.ParseInt(f[6], 10, 64)
			tx, _ := strconv.ParseInt(f[7], 10, 64)
			ps := PeerStatus{Rx: rx, Tx: tx}
			if f[3] != "(none)" {
				ps.Endpoint = f[3]
			}
			if hs > 0 {
				ps.LastHandshake = time.Unix(hs, 0)
			}
			st.Peers[f[1]] = ps
		}
	}
	return res
}

// AutostartEnabled reports whether wg-quick@name is enabled in systemd.
func AutostartEnabled(name string) bool {
	out, _ := run("systemctl", "is-enabled", "wg-quick@"+name)
	return strings.TrimSpace(out) == "enabled"
}

// SetAutostart enables or disables wg-quick@name at boot.
func SetAutostart(name string, on bool) error {
	verb := "disable"
	if on {
		verb = "enable"
	}
	_, err := run("systemctl", verb, "wg-quick@"+name)
	return err
}

// DefaultRouteIface returns the interface carrying the IPv4 default route.
func DefaultRouteIface() string {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) > 2 && f[1] == "00000000" && f[2] != "00000000" {
			return f[0]
		}
	}
	return ""
}

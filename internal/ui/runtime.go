package ui

import (
	"crypto/sha256"
	"time"

	"github.com/KsmBl/wgtui/internal/store"
	"github.com/KsmBl/wgtui/internal/wg"
)

// Runtime abstracts everything that touches the live system, so the UI
// can also run in demo mode without root.
type Runtime interface {
	IsUp(name string) bool
	Status() (map[string]*wg.IfaceStatus, error)
	Up(name, path string) error
	Down(name, path string) error
	Sync(name, path string) error
	Autostart(name string) bool
	SetAutostart(name string, on bool) error
}

// System is the real runtime using wg, wg-quick and systemctl.
type System struct{}

func (System) IsUp(n string) bool                          { return wg.IsUp(n) }
func (System) Status() (map[string]*wg.IfaceStatus, error) { return wg.Dump() }
func (System) Up(_, p string) error                        { return wg.Up(p) }
func (System) Down(_, p string) error                      { return wg.Down(p) }
func (System) Sync(n, p string) error                      { return wg.SyncConf(n, p) }
func (System) Autostart(n string) bool                     { return wg.AutostartEnabled(n) }
func (System) SetAutostart(n string, on bool) error        { return wg.SetAutostart(n, on) }

// Demo fakes a running system for screenshots and trying out the UI.
type Demo struct {
	Store   *store.Store
	up      map[string]bool
	auto    map[string]bool
	started time.Time
}

// NewDemo returns a demo runtime where the given interfaces are up.
func NewDemo(s *store.Store, up ...string) *Demo {
	d := &Demo{Store: s, up: map[string]bool{}, auto: map[string]bool{}, started: time.Now()}
	for _, n := range up {
		d.up[n] = true
		d.auto[n] = true
	}
	return d
}

func (d *Demo) IsUp(n string) bool                   { return d.up[n] }
func (d *Demo) Up(n, _ string) error                 { d.up[n] = true; return nil }
func (d *Demo) Down(n, _ string) error               { d.up[n] = false; return nil }
func (d *Demo) Sync(string, string) error            { return nil }
func (d *Demo) Autostart(n string) bool              { return d.auto[n] }
func (d *Demo) SetAutostart(n string, on bool) error { d.auto[n] = on; return nil }

// Status invents plausible, deterministic handshakes and traffic.
func (d *Demo) Status() (map[string]*wg.IfaceStatus, error) {
	res := map[string]*wg.IfaceStatus{}
	now := time.Now()
	for _, i := range d.Store.Ifaces {
		if !d.up[i.Name] {
			continue
		}
		st := &wg.IfaceStatus{ListenPort: i.ListenPort(), Peers: map[string]wg.PeerStatus{}}
		for n, p := range i.File.Peers {
			h := sha256.Sum256([]byte(p.PublicKey()))
			if n == 3 { // one offline client looks realistic
				continue
			}
			grow := int64(now.Sub(d.started).Seconds()) * int64(h[2]) * 40
			ps := wg.PeerStatus{
				LastHandshake: now.Add(-time.Duration(int(h[0])%110+5) * time.Second),
				Rx:            int64(h[1])*int64(h[3])*900 + grow,
				Tx:            int64(h[4])*int64(h[5])*2100 + grow*3,
				Endpoint:      p.Get("Endpoint"),
			}
			if ps.Endpoint == "" {
				ps.Endpoint = "203.0.113." + itoa(int(h[6])%250+2) + ":" + itoa(40000+int(h[7])*90)
			}
			st.Peers[p.PublicKey()] = ps
		}
		res[i.Name] = st
	}
	return res, nil
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}

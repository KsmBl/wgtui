package wgconf

import (
	"strings"
	"testing"
)

const sample = `# managed by some script
[Interface]
Address = 10.66.66.1/24,fd42::1/64
ListenPort = 51820
PrivateKey = aGVsbG8gd29ybGQgaGVsbG8gd29ybGQgaGVsbG8gd28=
PostUp = iptables -A FORWARD -i wg0 -j ACCEPT

### Client alice
[Peer]
PublicKey = YWxpY2VhbGljZWFsaWNlYWxpY2VhbGljZWFsaWNlYWw=
AllowedIPs = 10.66.66.2/32

[Peer]
# Name = bob
# laptop
PublicKey = Ym9iYm9iYm9iYm9iYm9iYm9iYm9iYm9iYm9iYm9iYm8=
AllowedIPs = 10.66.66.3/32
`

func TestParseRoundTrip(t *testing.T) {
	f, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Peers) != 2 || f.Peers[0].Name != "alice" || f.Peers[1].Name != "bob" {
		t.Fatalf("names: %+v", f.Peers)
	}
	if got := f.Interface.GetList("Address"); len(got) != 2 {
		t.Fatalf("addresses %v", got)
	}
	if f.Interface.Get("postup") == "" {
		t.Fatal("case-insensitive get")
	}
	f.Managed = []KV{{"PostUp", "echo hi"}}
	again, err := Parse(strings.NewReader(f.String()))
	if err != nil {
		t.Fatal(err)
	}
	if again.String() != f.String() {
		t.Fatalf("not stable:\n%s\n---\n%s", f, again)
	}
	if len(again.Managed) != 1 || again.Interface.Get("PostUp") != "iptables -A FORWARD -i wg0 -j ACCEPT" {
		t.Fatalf("managed block not separated: %+v", again.Managed)
	}
	if again.Peers[1].Comments[0] != "# laptop" {
		t.Fatalf("comments lost: %v", again.Peers[1].Comments)
	}
}

func TestParseErrors(t *testing.T) {
	for _, s := range []string{"Key = x\n", "[Peer]\nPublicKey = x\n", "[Interface]\nnonsense\n", "[Foo]\n"} {
		if _, err := Parse(strings.NewReader(s)); err == nil {
			t.Errorf("expected error for %q", s)
		}
	}
}

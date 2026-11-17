// Package wgconf parses and writes wg-quick style WireGuard configuration
// files. It keeps unknown keys, ordering and comments so that existing
// configurations survive a round trip through wgtui.
package wgconf

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// Markers that enclose lines wgtui generates itself (firewall rules).
// Everything between them is regenerated on every save.
const (
	ManagedBegin = "# wgtui:begin (generated, do not edit)"
	ManagedEnd   = "# wgtui:end"
)

// KV is a single "Key = Value" line.
type KV struct {
	Key   string
	Value string
}

// Section is an [Interface] or [Peer] block.
type Section struct {
	Kind     string   // "Interface" or "Peer"
	Name     string   // friendly peer name (stored as a comment)
	Comments []string // other comment lines, kept verbatim
	Entries  []KV
}

// File is a whole configuration file.
type File struct {
	Header    []string // comment lines before the first section
	Interface *Section
	Peers     []*Section
	// Managed holds lines found between the wgtui markers of [Interface].
	Managed []KV
}

var (
	nameComment   = regexp.MustCompile(`^#+\s*(?i:name|friendly_name|friendlyname)\s*[=:]\s*(.+?)\s*$`)
	clientComment = regexp.MustCompile(`^#{1,3}\s*(?i:client)\s+(.+?)\s*$`)
)

// Get returns the first value for key (case-insensitive).
func (s *Section) Get(key string) string {
	for _, e := range s.Entries {
		if strings.EqualFold(e.Key, key) {
			return e.Value
		}
	}
	return ""
}

// GetAll returns all values for key; comma separated values are split.
func (s *Section) GetList(key string) []string {
	var out []string
	for _, e := range s.Entries {
		if strings.EqualFold(e.Key, key) {
			for _, v := range strings.Split(e.Value, ",") {
				if v = strings.TrimSpace(v); v != "" {
					out = append(out, v)
				}
			}
		}
	}
	return out
}

// Set replaces the first occurrence of key (dropping further ones) or
// appends it. An empty value removes the key.
func (s *Section) Set(key, value string) {
	if value == "" {
		s.Del(key)
		return
	}
	found := false
	out := s.Entries[:0]
	for _, e := range s.Entries {
		if strings.EqualFold(e.Key, key) {
			if found {
				continue
			}
			found = true
			e.Value = value
		}
		out = append(out, e)
	}
	s.Entries = out
	if !found {
		s.Entries = append(s.Entries, KV{key, value})
	}
}

// Del removes all occurrences of key.
func (s *Section) Del(key string) {
	out := s.Entries[:0]
	for _, e := range s.Entries {
		if !strings.EqualFold(e.Key, key) {
			out = append(out, e)
		}
	}
	s.Entries = out
}

// PublicKey of a peer section.
func (s *Section) PublicKey() string { return s.Get("PublicKey") }

// ParseFile reads and parses path.
func ParseFile(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// Parse parses a configuration.
func Parse(r io.Reader) (*File, error) {
	file := &File{}
	var cur *Section
	var pending []string // comments seen since the last key line
	pendingName := ""
	inManaged := false
	sc := bufio.NewScanner(r)
	lineNo := 0
	flush := func() {
		if cur == nil {
			file.Header = append(file.Header, pending...)
		} else {
			cur.Comments = append(cur.Comments, pending...)
		}
		pending = nil
	}
	for sc.Scan() {
		lineNo++
		raw := strings.TrimRight(sc.Text(), "\r")
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			continue
		case line == ManagedBegin || strings.HasPrefix(line, "# wgtui:begin"):
			inManaged = true
			continue
		case line == ManagedEnd:
			inManaged = false
			continue
		case strings.HasPrefix(line, "#"):
			if m := clientComment.FindStringSubmatch(line); m != nil && !strings.Contains(m[1], "=") {
				// "### Client alice" style (wireguard-install scripts),
				// written before the [Peer] header.
				pendingName = m[1]
				continue
			}
			if m := nameComment.FindStringSubmatch(line); m != nil {
				if cur != nil && cur.Kind == "Peer" && cur.Name == "" && len(cur.Entries) == 0 {
					cur.Name = m[1]
				} else {
					pendingName = m[1]
				}
				continue
			}
			pending = append(pending, line)
			continue
		case strings.HasPrefix(line, "["):
			hdr := strings.Trim(line, "[] \t")
			sec := &Section{Kind: hdr}
			switch strings.ToLower(hdr) {
			case "interface":
				sec.Kind = "Interface"
				if file.Interface != nil {
					return nil, fmt.Errorf("line %d: duplicate [Interface]", lineNo)
				}
				file.Interface = sec
			case "peer":
				sec.Kind = "Peer"
				sec.Name = pendingName
				pendingName = ""
				file.Peers = append(file.Peers, sec)
			default:
				return nil, fmt.Errorf("line %d: unknown section %q", lineNo, hdr)
			}
			// Comments directly above a section header belong to it.
			if cur == nil {
				file.Header = append(file.Header, pending...)
			} else {
				sec.Comments = append(sec.Comments, pending...)
			}
			pending = nil
			inManaged = false
			cur = sec
			continue
		}
		if cur == nil {
			return nil, fmt.Errorf("line %d: key outside of a section", lineNo)
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected Key = Value", lineNo)
		}
		kv := KV{strings.TrimSpace(k), strings.TrimSpace(v)}
		if inManaged && cur.Kind == "Interface" {
			file.Managed = append(file.Managed, kv)
			continue
		}
		if cur.Kind == "Peer" && cur.Name == "" && pendingName != "" {
			cur.Name = pendingName
			pendingName = ""
		}
		flush()
		cur.Entries = append(cur.Entries, kv)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	flush()
	if file.Interface == nil {
		return nil, fmt.Errorf("no [Interface] section")
	}
	return file, nil
}

// Write serialises the file.
func (f *File) Write(w io.Writer) error {
	bw := bufio.NewWriter(w)
	for _, c := range f.Header {
		fmt.Fprintln(bw, c)
	}
	if len(f.Header) > 0 {
		fmt.Fprintln(bw)
	}
	writeSec := func(s *Section, managed []KV) {
		fmt.Fprintf(bw, "[%s]\n", s.Kind)
		if s.Name != "" {
			fmt.Fprintf(bw, "# Name = %s\n", s.Name)
		}
		for _, c := range s.Comments {
			fmt.Fprintln(bw, c)
		}
		for _, e := range s.Entries {
			fmt.Fprintf(bw, "%s = %s\n", e.Key, e.Value)
		}
		if len(managed) > 0 {
			fmt.Fprintln(bw, ManagedBegin)
			for _, e := range managed {
				fmt.Fprintf(bw, "%s = %s\n", e.Key, e.Value)
			}
			fmt.Fprintln(bw, ManagedEnd)
		}
	}
	if f.Interface != nil {
		writeSec(f.Interface, f.Managed)
	}
	for _, p := range f.Peers {
		fmt.Fprintln(bw)
		writeSec(p, nil)
	}
	return bw.Flush()
}

// String renders the file.
func (f *File) String() string {
	var sb strings.Builder
	_ = f.Write(&sb)
	return sb.String()
}

// WriteFile atomically writes the file with mode 0600.
func (f *File) WriteFile(path string) error {
	tmp := path + ".wgtui.tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := f.Write(out); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Peer returns the peer section with the given public key.
func (f *File) Peer(pub string) *Section {
	for _, p := range f.Peers {
		if p.PublicKey() == pub {
			return p
		}
	}
	return nil
}

// RemovePeer deletes the peer with the given public key.
func (f *File) RemovePeer(pub string) bool {
	for i, p := range f.Peers {
		if p.PublicKey() == pub {
			f.Peers = append(f.Peers[:i], f.Peers[i+1:]...)
			return true
		}
	}
	return false
}

// String renders a single section.
func (s *Section) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[%s]\n", s.Kind)
	if s.Name != "" {
		fmt.Fprintf(&sb, "# Name = %s\n", s.Name)
	}
	for _, e := range s.Entries {
		fmt.Fprintf(&sb, "%s = %s\n", e.Key, e.Value)
	}
	return sb.String()
}

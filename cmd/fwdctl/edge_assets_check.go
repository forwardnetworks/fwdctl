package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
)

// fwdctl edge-assets check reads the edge-assets.txt of a snapshot package (or the file alone) and says what Forward will do with it. The file is NUMBERED text-proto: every field carries its name AND its
// number (`edge_assets 1 {`, `name 2: "x"`, `ips 3 { address 1: "\012\000\000\001" }`, `mac_address 4 { addr 1: 2199023255553 }`, `cloud_host_id 5: "i-0"`, `attribution 6 { ... }`). A field written
// by name only (`name: "x"`) is silently ignored when the package is imported, and so are assets that share a MAC address (field report from a live import; not verified by this toolkit). The layout
// below is from a fixture that Forward imported and processed; whether `attribution` is required is not known.
//
// This command only READS: it never writes the package, and an import stays a separate explicit edit-snapshot write.

type eaToken struct {
	kind string // ident, num, str, {, }, :
	text string
	line int
}

func eaTokenize(src string) ([]eaToken, error) {
	var out []eaToken
	line := 1
	rs := []rune(src)
	for i := 0; i < len(rs); {
		r := rs[i]
		switch {
		case r == '\n':
			line++
			i++
		case unicode.IsSpace(r):
			i++
		case r == '#':
			for i < len(rs) && rs[i] != '\n' {
				i++
			}
		case r == '{' || r == '}' || r == ':':
			out = append(out, eaToken{string(r), string(r), line})
			i++
		case r == '"':
			j := i + 1
			var b strings.Builder
			for j < len(rs) && rs[j] != '"' {
				if rs[j] == '\\' && j+1 < len(rs) {
					j++
					// octal escape: up to three digits
					if rs[j] >= '0' && rs[j] <= '7' {
						n, k := 0, 0
						for k < 3 && j < len(rs) && rs[j] >= '0' && rs[j] <= '7' {
							n = n*8 + int(rs[j]-'0')
							j++
							k++
						}
						b.WriteByte(byte(n))
						continue
					}
					switch rs[j] {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					case 'r':
						b.WriteByte('\r')
					default:
						b.WriteRune(rs[j])
					}
					j++
					continue
				}
				b.WriteRune(rs[j])
				j++
			}
			if j >= len(rs) {
				return nil, fmt.Errorf("line %d: a string is not closed", line)
			}
			out = append(out, eaToken{"str", b.String(), line})
			i = j + 1
		case unicode.IsDigit(r) || r == '-':
			j := i + 1
			for j < len(rs) && (unicode.IsDigit(rs[j]) || rs[j] == 'x' || (rs[j] >= 'a' && rs[j] <= 'f') || (rs[j] >= 'A' && rs[j] <= 'F')) {
				j++
			}
			out = append(out, eaToken{"num", string(rs[i:j]), line})
			i = j
		case unicode.IsLetter(r) || r == '_':
			j := i + 1
			for j < len(rs) && (unicode.IsLetter(rs[j]) || unicode.IsDigit(rs[j]) || rs[j] == '_') {
				j++
			}
			out = append(out, eaToken{"ident", string(rs[i:j]), line})
			i = j
		default:
			return nil, fmt.Errorf("line %d: unexpected character %q", line, r)
		}
	}
	return out, nil
}

// eaField is one parsed field: its name, its number (0 when written by name only), and either a scalar value or children.
type eaField struct {
	name     string
	number   int
	line     int
	scalar   *eaToken
	children []*eaField
}

func eaParse(toks []eaToken) ([]*eaField, error) {
	pos := 0
	var block func(top bool) ([]*eaField, error)
	block = func(top bool) ([]*eaField, error) {
		var fs []*eaField
		for pos < len(toks) {
			t := toks[pos]
			if t.kind == "}" {
				if top {
					return nil, fmt.Errorf("line %d: an unexpected }", t.line)
				}
				pos++
				return fs, nil
			}
			if t.kind != "ident" {
				return nil, fmt.Errorf("line %d: expected a field name, found %q", t.line, t.text)
			}
			f := &eaField{name: t.text, line: t.line}
			pos++
			if pos < len(toks) && toks[pos].kind == "num" {
				n, err := strconv.Atoi(toks[pos].text)
				if err != nil {
					return nil, fmt.Errorf("line %d: %q is not a field number", toks[pos].line, toks[pos].text)
				}
				f.number = n
				pos++
			}
			if pos < len(toks) && toks[pos].kind == ":" {
				pos++
			}
			if pos >= len(toks) {
				return nil, fmt.Errorf("line %d: %s has no value", f.line, f.name)
			}
			switch toks[pos].kind {
			case "{":
				pos++
				kids, err := block(false)
				if err != nil {
					return nil, err
				}
				f.children = kids
			case "str", "num", "ident":
				v := toks[pos]
				f.scalar = &v
				pos++
			default:
				return nil, fmt.Errorf("line %d: %s has no value", f.line, f.name)
			}
			fs = append(fs, f)
		}
		if !top {
			return nil, fmt.Errorf("a { is never closed")
		}
		return fs, nil
	}
	return block(true)
}

type edgeAssetReport struct {
	Name        string   `json:"name"`
	Line        int      `json:"line"`
	IPs         []string `json:"ips,omitempty"`
	MAC         string   `json:"mac,omitempty"`
	CloudHostID string   `json:"cloud_host_id,omitempty"`
	Problems    []string `json:"problems,omitempty"`
}

type edgeAssetsCheck struct {
	Source        string            `json:"source"`
	Assets        []edgeAssetReport `json:"assets"`
	DuplicateMACs []map[string]any  `json:"duplicate_macs,omitempty"`
	DuplicateName []string          `json:"duplicate_names,omitempty"`
	NamedOnly     []int             `json:"named_field_lines,omitempty"`
	Problems      int               `json:"problems"`
}

func macText(v uint64) string {
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", byte(v>>40), byte(v>>32), byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func checkEdgeAssets(src string) (edgeAssetsCheck, error) {
	var rep edgeAssetsCheck
	toks, err := eaTokenize(src)
	if err != nil {
		return rep, err
	}
	top, err := eaParse(toks)
	if err != nil {
		return rep, err
	}
	named := map[int]bool{}
	var walk func(fs []*eaField)
	walk = func(fs []*eaField) {
		for _, f := range fs {
			if f.number == 0 {
				named[f.line] = true
			}
			walk(f.children)
		}
	}
	walk(top)
	macOwners := map[string][]string{}
	nameCount := map[string]int{}
	for _, f := range top {
		if f.name != "edge_assets" {
			rep.Assets = append(rep.Assets, edgeAssetReport{Name: "", Line: f.line, Problems: []string{fmt.Sprintf("line %d: a top-level field %q is not an edge asset", f.line, f.name)}})
			continue
		}
		a := edgeAssetReport{Line: f.line}
		if f.number == 0 {
			a.Problems = append(a.Problems, fmt.Sprintf("line %d: edge_assets is written without its field number (1): Forward ignores a field written by name only", f.line))
		}
		for _, k := range f.children {
			switch k.name {
			case "name":
				if k.scalar != nil {
					a.Name = k.scalar.text
				}
				if k.number == 0 {
					a.Problems = append(a.Problems, fmt.Sprintf("line %d: name is written without its field number (2)", k.line))
				}
			case "ips":
				for _, c := range k.children {
					if c.name != "address" || c.scalar == nil {
						continue
					}
					b := []byte(c.scalar.text)
					switch len(b) {
					case 4:
						a.IPs = append(a.IPs, fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3]))
					case 16:
						a.IPs = append(a.IPs, fmt.Sprintf("%x", b))
					default:
						a.Problems = append(a.Problems, fmt.Sprintf("line %d: an address is %d bytes (an IPv4 address is 4 bytes, IPv6 16)", c.line, len(b)))
					}
					if c.number == 0 {
						a.Problems = append(a.Problems, fmt.Sprintf("line %d: address is written without its field number (1)", c.line))
					}
				}
				if k.number == 0 {
					a.Problems = append(a.Problems, fmt.Sprintf("line %d: ips is written without its field number (3)", k.line))
				}
			case "mac_address":
				for _, c := range k.children {
					if c.name == "addr" && c.scalar != nil {
						n, err := strconv.ParseUint(c.scalar.text, 0, 64)
						if err != nil || n > 0xffffffffffff {
							a.Problems = append(a.Problems, fmt.Sprintf("line %d: addr %q is not a 48-bit integer", c.line, c.scalar.text))
							continue
						}
						a.MAC = macText(n)
						if c.number == 0 {
							a.Problems = append(a.Problems, fmt.Sprintf("line %d: addr is written without its field number (1)", c.line))
						}
					}
				}
				if k.number == 0 {
					a.Problems = append(a.Problems, fmt.Sprintf("line %d: mac_address is written without its field number (4)", k.line))
				}
			case "cloud_host_id":
				if k.scalar != nil {
					a.CloudHostID = k.scalar.text
				}
				if k.number == 0 {
					a.Problems = append(a.Problems, fmt.Sprintf("line %d: cloud_host_id is written without its field number (5)", k.line))
				}
			case "attribution":
				// who created or changed it and when: accepted by the importer, not checked here
			default:
				a.Problems = append(a.Problems, fmt.Sprintf("line %d: %q is not a field this check knows (name, ips, mac_address, cloud_host_id, attribution)", k.line, k.name))
			}
		}
		if strings.TrimSpace(a.Name) == "" {
			a.Problems = append(a.Problems, fmt.Sprintf("line %d: the asset has no name", f.line))
		}
		if len(a.IPs) == 0 && a.MAC == "" && a.CloudHostID == "" {
			a.Problems = append(a.Problems, "the asset has no address, MAC or cloud host id, so Forward cannot map it to a host")
		}
		if a.MAC != "" {
			macOwners[a.MAC] = append(macOwners[a.MAC], a.Name)
		}
		nameCount[strings.ToLower(a.Name)]++
		rep.Assets = append(rep.Assets, a)
	}
	for mac, names := range macOwners {
		if len(names) > 1 {
			sort.Strings(names)
			rep.DuplicateMACs = append(rep.DuplicateMACs, map[string]any{"mac": mac, "assets": names})
		}
	}
	sort.Slice(rep.DuplicateMACs, func(i, j int) bool {
		return rep.DuplicateMACs[i]["mac"].(string) < rep.DuplicateMACs[j]["mac"].(string)
	})
	for n, c := range nameCount {
		if c > 1 && n != "" {
			rep.DuplicateName = append(rep.DuplicateName, n)
		}
	}
	sort.Strings(rep.DuplicateName)
	for l := range named {
		rep.NamedOnly = append(rep.NamedOnly, l)
	}
	sort.Ints(rep.NamedOnly)
	for _, a := range rep.Assets {
		rep.Problems += len(a.Problems)
	}
	rep.Problems += len(rep.DuplicateMACs) + len(rep.DuplicateName)
	return rep, nil
}

// readEdgeAssetsFile returns the text of edge-assets.txt: the file itself, or the entry of that name in a snapshot ZIP.
func readEdgeAssetsFile(p string) (string, string, error) {
	if strings.HasSuffix(strings.ToLower(p), ".zip") {
		zr, err := zip.OpenReader(p)
		if err != nil {
			return "", "", err
		}
		defer zr.Close()
		for _, f := range zr.File {
			if path.Base(f.Name) == "edge-assets.txt" {
				rc, err := f.Open()
				if err != nil {
					return "", "", err
				}
				defer rc.Close()
				b, err := io.ReadAll(io.LimitReader(rc, 64<<20))
				return string(b), f.Name, err
			}
		}
		return "", "", fmt.Errorf("%s has no edge-assets.txt: the snapshot carries no edge assets (none were included when it was created, or EDGE_ASSETS_IN_SOURCES was off)", p)
	}
	b, err := os.ReadFile(p)
	return string(b), p, err
}

func (a *app) edgeAssetsCmd() *cobra.Command {
	c := &cobra.Command{
		Use: "edge-assets", GroupID: "nqe", Short: "check the edge assets file of a snapshot package before it is imported",
		Example: "  fwdctl edge-assets check snapshot.zip",
		Long:    "The edge assets of a network travel inside a snapshot package as edge-assets.txt. A package import KEEPS the package's own file (it does not save the network's current assets), so for a network that cannot be collected again the fix for assets missing from its snapshot is to edit that file and import the package as a new snapshot. The file is NUMBERED text-proto: every field carries its name and its number.",
	}
	c.AddCommand(&cobra.Command{
		Use: "check FILE", Short: "list the edge assets in a package (a .zip) or an edge-assets.txt and flag what Forward silently ignores",
		Example: "  fwdctl edge-assets check snapshot.zip\n  fwdctl edge-assets check edge-assets.txt",
		Long:    "Reads a snapshot package (a .zip: its edge-assets.txt is found inside) or an edge-assets.txt and prints JSON: each asset with its name, addresses, MAC and cloud host id decoded, and the problems. It flags a field written by name only (Forward ignores it: no error, the asset never appears), assets that share a MAC address (a live report says Forward maps neither), an asset with no name or nothing to map it by, a duplicate name and an address that is not 4 or 16 bytes. Exit status 0 when nothing is flagged, 1 when something is, 64 for a file that cannot be read or parsed. Offline and read-only: it never writes the package; importing is a separate edit-snapshot write. The layout is from a file Forward imported and processed; that a flagged item is dropped by Forward is a field report, not verified by this tool.",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			text, src, err := readEdgeAssetsFile(args[0])
			if err != nil {
				fmt.Fprintf(a.err, "error: %v\n", err)
				return a.exit(64)
			}
			rep, err := checkEdgeAssets(text)
			if err != nil {
				fmt.Fprintf(a.err, "error: %s does not parse as text-proto: %v\n", src, err)
				return a.exit(64)
			}
			rep.Source = src
			b, _ := json.MarshalIndent(rep, "", "  ")
			fmt.Fprintln(a.out, string(b))
			if rep.Problems > 0 {
				return a.exit(1)
			}
			return nil
		},
	})
	return c
}

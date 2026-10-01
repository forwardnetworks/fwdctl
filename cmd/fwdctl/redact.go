package main

// DOGFOOD-TEMP: fwdctl redact-check is the mechanical safety net of the temporary plan-report-skill-gap playbook. Remove it with the
// playbook (see docs/internal.md). It is fully offline and never prints a suspected secret or identifier back unmasked.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	osuser "os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/skills"
)

type redactFinding struct {
	Kind    string `json:"kind"`
	Line    int    `json:"line"`
	Excerpt string `json:"excerpt"`
	Advice  string `json:"advice"`
}

type redactReport struct {
	OK       bool            `json:"ok"` // true only with no findings and no warnings
	Findings []redactFinding `json:"findings"`
	Warnings []redactFinding `json:"warnings"`
	Note     string          `json:"note"`
}

// maskExcerpt keeps at most the first two characters of what was found (fewer for short text, so little of a short secret survives).
func maskExcerpt(s string) string {
	s = strings.TrimSpace(s)
	n := len([]rune(s))
	keep := 0
	switch {
	case n >= 8:
		keep = 2
	case n >= 4:
		keep = 1
	}
	return string([]rune(s)[:keep]) + "***"
}

var (
	reNoteRef     = regexp.MustCompile(`(?i)private reproduction details saved locally by the reporter, ref ([a-z0-9-]{3,40})`)
	rePlaceholder = regexp.MustCompile(`<[A-Za-z][A-Za-z0-9_ -]{0,40}>`)
	reURL         = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s)>\]"'` + "`" + `]+`)
	reEmail       = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+`)
	rePEM         = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]+-----`)
	reAWS         = regexp.MustCompile(`\b(?:AKIA|ASIA|AGPA|AIDA|AROA)[0-9A-Z]{16}\b`)
	reBearer      = regexp.MustCompile(`(?i)\bbearer\s+([A-Za-z0-9._~+/=-]{8,})`)
	reBasic       = regexp.MustCompile(`(?i)\bbasic\s+([A-Za-z0-9+/=]{8,})`)
	reKeyword     = regexp.MustCompile(`(?i)\b(?:password|passwd|pwd|secret|token|api[_ -]?key|apikey|credentials?|bearer)\b["']?([ \t]*[:=][ \t]*|[ \t]+(?:is[ \t]+)?)["']?([^\s"',;]{6,})`)
	reAuthHeader  = regexp.MustCompile(`(?i)\bauthorization["']?[ \t]*[:=][ \t]*["']?(?:bearer|basic|token|digest)[ \t]+([^\s"',;]{6,})`)
	reCredURL     = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s/@:]+:([^\s/@]+)@`)
	reCustomer    = regexp.MustCompile(`\b(?:[Cc]ustomer|[Cc]lient)[ \t]+([A-Za-z0-9][A-Za-z0-9&.'-]*(?:[ \t]+[A-Za-z0-9][A-Za-z0-9&.'-]*)?)`)
	reCapName     = regexp.MustCompile(`\b(?:[Cc]ustomer|[Cc]lient|[Ff]or|[Aa]t|[Ff]rom|[Ww]ith)[ \t]+([A-Z][A-Za-z0-9&.'-]*(?:[ \t]+[A-Z][A-Za-z0-9&.'-]*){0,2})`)
	reHome        = regexp.MustCompile(`(?i)(?:/home/[^\s/"'` + "`" + `]+|/Users/[^\s/"'` + "`" + `]+|[A-Z]:\\Users\\[^\s\\"'` + "`" + `]+)`)
	reMAC         = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{2}[:-]){5}[0-9a-f]{2}\b|\b[0-9a-f]{4}\.[0-9a-f]{4}\.[0-9a-f]{4}\b`)
	reIPv4        = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?:/\d{1,2})?`)
	reIPv6        = regexp.MustCompile(`(?i)(?:[0-9a-f]{0,4}:){2,7}[0-9a-f]{0,4}(?:/\d{1,3})?`)
	reHost        = regexp.MustCompile(`\b[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)+\b`)
	reDevice      = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9]*(?:-[A-Za-z0-9]+){2,}\b`)
	reDateish     = regexp.MustCompile(`^[A-Za-z]+-\d{4}-\d{2}(?:-\d{2})?$`)
	reNumericID   = regexp.MustCompile(`(?i)\b(?:network|snapshot|org|organization|oid|tenant|deployment|task)(?:[_ -]?id)?["']?(?:[ \t]*[:=#][ \t]*|[ \t]+)["']?(\d{2,})(\.\d)?`)
	reOwnRepo     = regexp.MustCompile(`(?i)https://github\.com/forwardnetworks/(?:forward-skills|fwdctl)[^\s)>\]"']*`)
	reHex         = regexp.MustCompile(`\b[0-9A-Fa-f]{24,}\b`)
	reB64         = regexp.MustCompile(`[A-Za-z0-9+/=_]{20,}`)
)

var docPrefixes = []netip.Prefix{
	netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("2001:db8::/32"),
}

func isDocAddr(a netip.Addr) bool {
	for _, p := range docPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

var allowedHosts = []string{"github.com", "githubusercontent.com", "forwardnetworks.com", "example.com", "example.org", "example.net", "anthropic.com", "claude.ai"}

func hostAllowed(h string) bool {
	h = strings.ToLower(h)
	for _, a := range allowedHosts {
		if h == a || strings.HasSuffix(h, "."+a) {
			return true
		}
	}
	return false
}

// plausibleTLD are the final labels that make a two-label dotted name a hostname (a file name such as internal.md is not).
var plausibleTLD = setOf("com", "net", "org", "edu", "gov", "mil", "int", "io", "co", "us", "uk", "de", "fr", "nl", "se", "no", "dk", "fi", "es", "it", "ch", "at", "be", "ca", "au", "jp", "cn", "in", "br", "ru", "app", "dev", "cloud", "ai", "biz", "info", "xyz", "tech", "online", "site",
	"local", "lan", "internal", "corp", "intranet", "home", "private", "localdomain", "lab", "test", "invalid", "localhost")

// fileExt are final labels of dotted names with three or more labels that are file names, not hosts.
var fileExt = setOf("md", "json", "go", "yml", "yaml", "txt", "sh", "py", "js", "ts", "log", "csv", "html", "mod", "sum", "toml", "zip", "gz", "tar", "tgz", "exe", "patch", "cfg", "conf", "ini", "pdf", "png", "jpg", "svg", "xml", "tf", "token", "diff", "rs", "c", "h", "bak", "tmp", "lock", "nqe", "jsonl", "tsv", "rb", "java", "css", "sql", "pem", "crt", "key", "out", "orig", "rej", "swp", "bin")

func setOf(v ...string) map[string]bool {
	m := map[string]bool{}
	for _, s := range v {
		m[s] = true
	}
	return m
}

func onlyLetters(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return s != ""
}

// redactor holds what is needed to check one draft.
type redactor struct {
	deny   []string         // normalised words (letters and digits only); never printed
	denyRE []*regexp.Regexp // one per deny word: separators between characters are optional, so acme-corp, "acme corp" and AcmeCorp all match
	skills map[string]bool
}

func newRedactor(deny []string) *redactor {
	r := &redactor{skills: map[string]bool{}}
	for _, n := range skills.Names() {
		r.skills[strings.ToLower(n)] = true
	}
	seen := map[string]bool{}
	for _, d := range deny {
		d = normDeny(d)
		if len(d) < 2 || seen[d] {
			continue
		}
		seen[d] = true
		r.deny = append(r.deny, d)
		var parts []string
		for _, c := range d {
			parts = append(parts, regexp.QuoteMeta(string(c)))
		}
		r.denyRE = append(r.denyRE, regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(`+strings.Join(parts, `[ _.-]?`)+`)(?:$|[^A-Za-z0-9])`))
	}
	return r
}

func blank(line string, a, b int) string {
	return line[:a] + strings.Repeat(" ", b-a) + line[b:]
}

// scrub runs one regexp over the working line, reports each match the keep function accepts as a finding, and blanks every match so a
// later detector cannot report the same text again. group selects the part of the match that is reported (0 is the whole).
func scrub(line *string, re *regexp.Regexp, group int, ln int, kind, advice string, out *[]redactFinding, accept func(m string, sub []string) bool) {
	for {
		changed := false
		for _, loc := range re.FindAllStringSubmatchIndex(*line, -1) {
			whole := (*line)[loc[0]:loc[1]]
			var sub []string
			for i := 0; i+1 < len(loc); i += 2 {
				if loc[i] < 0 {
					sub = append(sub, "")
				} else {
					sub = append(sub, (*line)[loc[i]:loc[i+1]])
				}
			}
			text := sub[group]
			ok := accept == nil || accept(whole, sub)
			if ok && text != "" {
				*out = append(*out, redactFinding{kind, ln, maskExcerpt(text), advice})
			}
			if ok {
				*line = blank(*line, loc[0], loc[1])
				changed = true
				break // offsets are stale after a blank: rescan
			}
		}
		if !changed {
			return
		}
	}
}

func (r *redactor) checkLine(orig string, ln int) (out, warns []redactFinding) {
	line := rePlaceholder.ReplaceAllStringFunc(orig, func(s string) string { return strings.Repeat(" ", len(s)) })

	for _, re := range r.denyRE {
		for re.MatchString(line) {
			loc := re.FindStringSubmatchIndex(line)
			out = append(out, redactFinding{"deny-list", ln, maskExcerpt(line[loc[2]:loc[3]]), "this word identifies the engineer's customer or login; delete it or use a placeholder such as <customer>, <user> or <forward-url>"})
			line = blank(line, loc[2], loc[3])
		}
	}

	// The one thing allowed to look like a device name: the generic reference slug of a private note (see dogfood-note). Deny words ran first.
	if loc := reNoteRef.FindStringSubmatchIndex(line); loc != nil {
		line = blank(line, loc[2], loc[3])
	}

	// This project's own repository URLs are the one URL that may appear in a draft; blank them first so the encoded-string detector does not read a path as a token.
	for _, loc := range reOwnRepo.FindAllStringIndex(line, -1) {
		line = blank(line, loc[0], loc[1])
	}

	const ph = " Replace it with a placeholder or delete the sentence."
	scrub(&line, reCredURL, 1, ln, "secret", "a URL with a user:password."+ph, &out, nil)
	scrub(&line, reURL, 0, ln, "url", "a URL other than this repository's own."+ph+" Use <forward-url> for a Forward instance.", &out, func(m string, _ []string) bool {
		lm := strings.ToLower(m)
		return !strings.HasPrefix(lm, "https://github.com/forwardnetworks/fwdctl") && !strings.HasPrefix(lm, "https://github.com/forwardnetworks/fwdctl")
	})
	scrub(&line, reEmail, 0, ln, "email", "an email address."+ph, &out, func(m string, _ []string) bool {
		return !hostAllowed(m[strings.LastIndex(m, "@")+1:]) || true // any address is a person: flag even on allowed domains
	})
	scrub(&line, rePEM, 0, ln, "secret", "a PEM key header."+ph, &out, nil)
	scrub(&line, reAWS, 0, ln, "secret", "an AWS-style access key."+ph, &out, nil)
	scrub(&line, reAuthHeader, 1, ln, "secret", "an Authorization header value."+ph, &out, nil)
	scrub(&line, reBearer, 1, ln, "secret", "a bearer credential."+ph, &out, nil)
	scrub(&line, reBasic, 1, ln, "secret", "a basic-auth credential."+ph, &out, nil)
	// A keyword followed by a value of 6+ characters. Prose such as "token: none" or "no password" passes: after a space or "is" the value must contain a
	// digit or mixed case; after = or : it must also be 8+ characters if it is plain lowercase.
	scrub(&line, reKeyword, 2, ln, "secret", "a value after password, secret, token, credential or api key."+ph, &out, func(_ string, sub []string) bool {
		v := sub[2]
		if hasDigit(v) || mixedCase(v) {
			return true
		}
		return strings.ContainsAny(sub[1], ":=") && len(v) >= 8
	})
	scrub(&line, reHome, 0, ln, "home-path", "a path with a user name. Use <user> or a generic path like ~/file."+ph, &out, nil)
	scrub(&line, reMAC, 0, ln, "mac", "a MAC address."+ph, &out, nil)
	scrub(&line, reIPv4, 0, ln, "ip", "an IP address or prefix; only 192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24 and 2001:db8::/32 are allowed. Use <ip>.", &out, func(m string, _ []string) bool {
		if p, err := netip.ParsePrefix(m); err == nil {
			return !isDocAddr(p.Addr())
		}
		if a, err := netip.ParseAddr(m); err == nil {
			return !isDocAddr(a)
		}
		return false // not an address (octet over 255)
	})
	for { // IPv6 needs a boundary check the regexp cannot express
		found := false
		for _, loc := range reIPv6.FindAllStringIndex(line, -1) {
			if loc[0] > 0 && strings.ContainsAny(line[loc[0]-1:loc[0]], "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ:.") {
				continue
			}
			if loc[1] < len(line) && strings.ContainsAny(line[loc[1]:loc[1]+1], "0123456789abcdefghijklmnopqrstuvwxyzGHIJKLMNOPQRSTUVWXYZ:") {
				continue
			}
			m := line[loc[0]:loc[1]]
			var a netip.Addr
			if p, err := netip.ParsePrefix(m); err == nil {
				a = p.Addr()
			} else if x, err := netip.ParseAddr(m); err == nil {
				a = x
			} else {
				continue
			}
			if !strings.ContainsAny(m, "0123456789abcdefABCDEF") {
				continue // "::" alone
			}
			if !isDocAddr(a) {
				out = append(out, redactFinding{"ip", ln, maskExcerpt(m), "an IP address or prefix; only 192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24 and 2001:db8::/32 are allowed. Use <ip>."})
			}
			line = blank(line, loc[0], loc[1])
			found = true
			break
		}
		if !found {
			break
		}
	}
	scrub(&line, reHost, 0, ln, "hostname", "a host or domain name. Use <device> or <forward-url>."+ph, &out, func(m string, _ []string) bool {
		if hostAllowed(m) {
			return false
		}
		labels := strings.Split(m, ".")
		tld := strings.ToLower(labels[len(labels)-1])
		if !onlyLetters(tld) || len(tld) < 2 {
			return false // v0.5.21, 1.0.0, e.g.
		}
		if plausibleTLD[tld] {
			return true
		}
		return len(labels) >= 3 && !fileExt[tld]
	})
	scrub(&line, reDevice, 0, ln, "device-name", "this looks like a device hostname. Use <device>.", &out, func(m string, _ []string) bool {
		if len(m) < 8 || !strings.ContainsAny(m, "0123456789") || r.skills[strings.ToLower(m)] || reDateish.MatchString(m) {
			return false
		}
		return true
	})
	scrub(&line, reNumericID, 1, ln, "numeric-id", "a network, snapshot, org, tenant, deployment or task id. Use <network-id>, <snapshot-id> or similar.", &out, func(_ string, sub []string) bool {
		return sub[2] == "" // 10.0 after "network" is a version, not an id
	})
	scrub(&line, reHex, 0, ln, "secret", "a long hex string (a key, hash or id)."+ph+" Use a 7-character commit prefix if you need a commit.", &out, nil)
	// 20+ characters: a digit and a letter; 20 to 23 characters must also be mixed case (fewer identifiers look like keys).
	scrub(&line, reB64, 0, ln, "secret", "a long encoded string (a key or token)."+ph, &out, func(m string, _ []string) bool {
		if r.skills[strings.ToLower(m)] || !hasDigit(m) || !strings.ContainsAny(m, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			return false
		}
		return len(m) >= 24 || mixedCase(m)
	})
	warns = customerWarnings(line, ln)
	return out, warns
}

func hasDigit(s string) bool { return strings.ContainsAny(s, "0123456789") }

func mixedCase(s string) bool {
	return strings.ContainsAny(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") && strings.ContainsAny(s, "abcdefghijklmnopqrstuvwxyz")
}

// productWords are capitalised words that may follow customer, for, at, from or with without looking like a customer or place name.
var productWords = setOf("forward", "networks", "github", "claude", "codex", "gemini", "kubernetes", "linux", "cisco", "juniper", "arista", "nx-os", "python", "go", "nqe", "predict", "snapshot", "snapshots", "skill", "skills", "api", "cli", "url", "json", "yaml", "fortinet", "palo", "alto", "pan-os", "ios", "ios-xe", "ios-xr", "junos", "eos", "aws", "azure", "gcp", "slack", "docker", "helm", "git", "windows", "macos", "ubuntu", "debian", "anthropic", "opus", "sonnet", "haiku", "netlab", "code", "desktop", "mac", "openshift", "vmware", "vsphere", "gitops", "bgp", "ospf", "vxlan", "aci", "sd-wan", "tls", "ssh", "snmp",
	"the", "this", "that", "these", "those", "example", "instance", "each", "every", "any", "all", "some", "now", "then", "if", "when", "step", "steps", "case", "details", "more", "least", "most", "first", "second", "last", "once", "both", "one", "two", "it", "its", "a", "an", "i", "we", "you", "they", "he", "she", "what", "which", "who", "how", "why", "where", "there", "here", "no", "yes", "not", "none", "other", "another", "such", "same", "least", "least", "lack", "unknown", "reference", "context", "reasons", "reason")

// lowerStop are the lower-case words that may follow "customer" or "client" in ordinary prose.
var lowerStop = setOf("the", "a", "an", "is", "are", "was", "were", "has", "have", "had", "said", "says", "asked", "reported", "wants", "want", "wanted", "who", "whose", "that", "this", "their", "his", "her", "it", "its", "on", "in", "of", "to", "and", "or", "but", "not", "no", "with", "without", "for", "at", "from", "by", "as", "if", "data", "side", "facing", "environment", "name", "names", "network", "networks", "snapshot", "snapshots", "org", "organization", "organisation", "tenant", "deployment", "lab", "labs", "site", "sites", "device", "devices", "request", "requests", "report", "reports", "issue", "issues", "task", "tasks", "case", "cases", "support", "team", "engineer", "engineers", "specifics", "specific", "details", "detail", "config", "configs", "configuration", "identifiers", "identifier", "ids", "id", "information", "input", "inputs", "output", "outputs", "will", "would", "can", "could", "may", "might", "should", "must", "does", "did", "do", "ran", "hit", "saw", "got", "sees", "uses", "used", "using", "setup", "setups", "question", "questions", "problem", "problems", "workflow", "scenario", "scenarios", "example", "examples", "instance", "instances", "account", "accounts", "base", "specific.", "side.")

// customerWarnings reports soft findings: a name right after customer/client (any case), or a capitalised name after for, at, from or with.
func customerWarnings(line string, ln int) []redactFinding {
	const adv = "looks like a customer or place name: replace with <customer> unless it is a Forward product word"
	var w []redactFinding
	var names []string
	add := func(text string) { names = append(names, text) }
	for _, m := range reCustomer.FindAllStringSubmatch(line, -1) {
		var name []string
		for _, word := range strings.Fields(m[1]) {
			if lowerStop[strings.ToLower(strings.Trim(word, ".,;:'"))] || productWords[strings.ToLower(strings.Trim(word, ".,;:'"))] {
				break
			}
			name = append(name, word)
		}
		if len(name) > 0 {
			add(strings.Join(name, " "))
		}
	}
	for _, m := range reCapName.FindAllStringSubmatch(line, -1) {
		var name []string
		bad := false
		for _, word := range strings.Fields(m[1]) {
			word = strings.Trim(word, ".,;:'")
			if word == "" {
				continue
			}
			if !productWords[strings.ToLower(word)] {
				bad = true
			}
			name = append(name, word)
		}
		if bad {
			add(strings.Join(name, " "))
		}
	}
	for i, n := range names { // one warning per name: drop a name that a longer one on the line already contains
		dup := false
		for j, o := range names {
			if i != j && strings.Contains(o, n) && (len(o) > len(n) || j < i) {
				dup = true
			}
		}
		if !dup {
			w = append(w, redactFinding{"possible-customer-name", ln, maskExcerpt(n), adv})
		}
	}
	return w
}

// normDeny keeps letters and digits only, lower-cased: acme-corp, "Acme Corp" and AcmeCorp are one entry.
func normDeny(d string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(d) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func (r *redactor) check(text string) []redactFinding {
	out, _ := r.checkAll(text)
	return out
}

// checkAll returns the hard findings and the soft warnings, each ordered by line.
func (r *redactor) checkAll(text string) (out, warns []redactFinding) {
	out, warns = []redactFinding{}, []redactFinding{}
	for i, l := range strings.Split(text, "\n") {
		f, w := r.checkLine(strings.TrimRight(l, "\r"), i+1)
		out = append(out, f...)
		warns = append(warns, w...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out, warns
}

// readTokenIdentity reads the FIRST TWO lines of a token file (URL, username) one byte at a time, so nothing after the second newline
// is ever read from the file, let alone kept. The third line is the secret.
func readTokenIdentity(r io.Reader) (urlLine, user string) {
	var lines []string
	var cur []byte
	buf := make([]byte, 1)
	for len(lines) < 2 {
		n, err := r.Read(buf)
		if n == 1 {
			if buf[0] == '\n' {
				if s := strings.TrimSpace(string(cur)); s != "" {
					lines = append(lines, s)
				}
				cur = cur[:0]
			} else {
				cur = append(cur, buf[0])
			}
		}
		if err != nil {
			if s := strings.TrimSpace(string(cur)); s != "" && len(lines) < 2 {
				lines = append(lines, s)
			}
			break
		}
	}
	for len(lines) < 2 {
		lines = append(lines, "")
	}
	return lines[0], lines[1]
}

var genericLogin = setOf("admin", "root", "user", "test", "guest", "forward", "fwd", "www", "app", "localhost", "https", "http")

// identityWords turns the saved login's URL and username into deny words: the host, its leading label, the username, an email's local part.
func identityWords(rawURL, user string) []string {
	var w []string
	if rawURL != "" {
		if !strings.Contains(rawURL, "://") {
			rawURL = "https://" + rawURL
		}
		if u, err := url.Parse(rawURL); err == nil && u.Hostname() != "" {
			h := strings.ToLower(u.Hostname())
			w = append(w, h)
			if first := strings.Split(h, ".")[0]; len(first) >= 3 && !genericLogin[first] && onlyAlnumDash(first) && !isDigits(first) {
				w = append(w, first)
			}
		}
	}
	if user = strings.TrimSpace(user); user != "" && !genericLogin[strings.ToLower(user)] {
		w = append(w, user)
		if i := strings.Index(user, "@"); i > 2 {
			if local := user[:i]; !genericLogin[strings.ToLower(local)] {
				w = append(w, local)
			}
		}
	}
	return w
}

func onlyAlnumDash(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// savedLoginDeny loads the deny words from the saved config (and the first two lines of its token file) and from FORWARD_URL / FORWARD_USERNAME.
// It never reads a password.
func savedLoginDeny(cfgPath string) (words []string, found bool) {
	var c connFile
	if cfgPath != "" {
		if b, err := os.ReadFile(cfgPath); err == nil && json.Unmarshal(b, &c) == nil {
			found = true
		}
	}
	words = append(words, identityWords(c.URL, c.Username)...)
	words = append(words, c.RedactDeny...)
	if len(c.RedactDeny) > 0 {
		found = true
	}
	if c.TokenFile != "" {
		p := c.TokenFile
		if strings.HasPrefix(p, "~/") {
			if h, err := os.UserHomeDir(); err == nil {
				p = filepath.Join(h, p[2:])
			}
		}
		if f, err := os.Open(p); err == nil {
			u, n := readTokenIdentity(f)
			f.Close()
			words = append(words, identityWords(u, n)...)
			found = true
		}
	}
	if u, n := os.Getenv("FORWARD_URL"), os.Getenv("FORWARD_USERNAME"); u != "" || n != "" {
		words = append(words, identityWords(u, n)...)
		found = true
	}
	return words, found
}

// machineIdentity is the OS user, the machine's host name (and its first label) and the home directory's last element: words that identify
// the engineer and appear in paths and prompts. A variable so tests can replace it.
var machineIdentity = realMachineIdentity

func realMachineIdentity() []string {
	var w []string
	add := func(x string) {
		x = strings.TrimSpace(x)
		if len(x) >= 3 && !genericLogin[strings.ToLower(x)] {
			w = append(w, x)
		}
	}
	if u, err := osuser.Current(); err == nil {
		add(u.Username)
		if i := strings.LastIndex(u.Username, `\`); i >= 0 { // DOMAIN\name on Windows
			add(u.Username[i+1:])
		}
	}
	add(os.Getenv("USER"))
	if h, err := os.Hostname(); err == nil {
		add(h)
		add(strings.Split(h, ".")[0])
	}
	if h, err := os.UserHomeDir(); err == nil {
		add(filepath.Base(h))
	}
	return w
}

const redactUsage = "usage: fwdctl redact-check [--file FILE | -] [--deny word,word,...]"

// redactCheckCmd is `fwdctl redact-check`. cfgPath is the saved-login config to build the automatic deny list from ("" for none).
func redactCheckCmd(args []string, stdin io.Reader, stdout, stderr io.Writer, cfgPath string) int {
	file, fromStdin := "", false
	var deny []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, val, hasVal := strings.Cut(a, "=")
		take := func() (string, bool) {
			if hasVal {
				return val, true
			}
			if i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		switch {
		case a == "-":
			fromStdin = true
		case name == "--file":
			v, ok := take()
			if !ok {
				fmt.Fprintln(stderr, redactUsage)
				return usage
			}
			if v == "-" {
				fromStdin = true
			} else {
				file = v
			}
		case name == "--deny":
			v, ok := take()
			if !ok {
				fmt.Fprintln(stderr, redactUsage)
				return usage
			}
			deny = append(deny, strings.Split(v, ",")...)
		default:
			fmt.Fprintln(stderr, redactUsage)
			return usage
		}
	}
	if (file == "") == !fromStdin || (file != "" && fromStdin) {
		fmt.Fprintln(stderr, redactUsage)
		return usage
	}
	var raw []byte
	var err error
	if fromStdin {
		raw, err = io.ReadAll(stdin)
	} else {
		raw, err = os.ReadFile(file)
	}
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return usage
	}
	saved, found := savedLoginDeny(cfgPath)
	deny = append(deny, saved...)
	deny = append(deny, machineIdentity()...)
	deny = append(deny, strings.Split(os.Getenv("FWDCTL_REDACT_DENY"), ",")...)
	rd := newRedactor(deny)
	findings, warnings := rd.checkAll(string(raw))
	note := "A clean check is necessary, not sufficient: the engineer must still read the draft and say yes, and raw skill output never goes into an issue even if the check passes. The tool cannot know customer, organization or network names it was not given: pass each one the session knows with --deny. Exit 1 = findings (fix them); exit 2 = warnings only (a possible customer or place name: fix it or ask the engineer); exit 0 = nothing found."
	if !found {
		note += " No saved login was found, so the login part of the automatic deny list was empty."
	}
	note += fmt.Sprintf(" The deny list had %d word(s) from the saved login, this machine's user and host, config redact_deny, FWDCTL_REDACT_DENY and --deny (not shown).", len(rd.deny))
	rep := redactReport{OK: len(findings) == 0 && len(warnings) == 0, Findings: findings, Warnings: warnings, Note: note}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(rep)
	if len(findings) > 0 {
		return 1
	}
	if len(warnings) > 0 {
		return 2
	}
	return 0
}

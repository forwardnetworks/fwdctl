package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// roff escapes text for a man page: backslashes and hyphens (so a flag stays copyable), and a leading dot or quote that would start a request.
func roff(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "-", `\-`)
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, ".") || strings.HasPrefix(l, "'") {
			l = `\&` + l
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func manName(c *cobra.Command) string { return strings.ReplaceAll(c.CommandPath(), " ", "-") }

func manFlags(b *strings.Builder, fs *pflag.FlagSet) {
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		b.WriteString(".TP\n.B ")
		if f.Shorthand != "" {
			fmt.Fprintf(b, `\-%s, `, roff(f.Shorthand))
		}
		fmt.Fprintf(b, `\-\-%s`, roff(f.Name))
		if t := f.Value.Type(); t != "bool" {
			fmt.Fprintf(b, " \\fI%s\\fR", roff(t))
		}
		b.WriteString("\n" + roff(f.Usage))
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" && f.DefValue != "[]" {
			fmt.Fprintf(b, " (default %s)", roff(f.DefValue))
		}
		b.WriteString("\n")
	})
}

// manPage renders one command as a man(1) page.
func manPage(c *cobra.Command, when time.Time) string {
	var b strings.Builder
	name := manName(c)
	fmt.Fprintf(&b, ".TH \"%s\" \"1\" \"%s\" \"fwdctl %s\" \"fwdctl manual\"\n", strings.ToUpper(name), when.Format("Jan 2006"), roff(version))
	fmt.Fprintf(&b, ".SH NAME\n%s \\- %s\n", roff(name), roff(c.Short))
	fmt.Fprintf(&b, ".SH SYNOPSIS\n.B %s\n", roff(c.UseLine()))
	desc := strings.TrimSpace(c.Long)
	if desc == "" {
		desc = c.Short
	}
	b.WriteString(".SH DESCRIPTION\n")
	for i, para := range strings.Split(desc, "\n\n") {
		if i > 0 {
			b.WriteString(".PP\n")
		}
		b.WriteString(roff(strings.TrimSpace(para)) + "\n")
	}
	if fl := c.LocalFlags(); fl.HasAvailableFlags() {
		b.WriteString(".SH OPTIONS\n")
		manFlags(&b, fl)
	}
	if c.HasParent() {
		if pf := c.InheritedFlags(); pf.HasAvailableFlags() {
			b.WriteString(".SH INHERITED OPTIONS\n")
			manFlags(&b, pf)
		}
	}
	if c.Example != "" {
		b.WriteString(".SH EXAMPLES\n.nf\n" + roff(strings.TrimRight(c.Example, "\n")) + "\n.fi\n")
	}
	var see []string
	if c.HasParent() {
		see = append(see, manName(c.Parent())+"(1)")
	}
	subs := append([]*cobra.Command{}, c.Commands()...)
	sort.Slice(subs, func(i, j int) bool { return subs[i].Name() < subs[j].Name() })
	for _, s := range subs {
		if !s.Hidden && s.Name() != "help" && s.Name() != "completion" {
			see = append(see, manName(s)+"(1)")
		}
	}
	if len(see) > 0 {
		b.WriteString(".SH SEE ALSO\n" + roff(strings.Join(see, ", ")) + "\n")
	}
	return b.String()
}

// writeManPages writes one man page per command under dir (fwdctl.1, fwdctl-nqe.1, fwdctl-nqe-run.1, ...) and returns how many.
func writeManPages(root *cobra.Command, dir string, when time.Time) (int, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	n := 0
	var walk func(c *cobra.Command) error
	walk = func(c *cobra.Command) error {
		if err := os.WriteFile(filepath.Join(dir, manName(c)+".1"), []byte(manPage(c, when)), 0o644); err != nil {
			return err
		}
		n++
		for _, s := range c.Commands() {
			if s.Hidden || s.Name() == "help" || s.Name() == "completion" {
				continue
			}
			if err := walk(s); err != nil {
				return err
			}
		}
		return nil
	}
	return n, walk(root)
}

// manCmd is `fwdctl man DIR`: write the man pages (the release build does this and ships them; a Homebrew install puts them in man1).
func (a *app) manCmd() *cobra.Command {
	return &cobra.Command{
		Use: "man DIR", Hidden: true, Short: "write man pages, one per command, into DIR", Args: cobra.ExactArgs(1),
		Example: "  fwdctl man ./man",
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := writeManPages(cmd.Root(), args[0], time.Now())
			if err != nil {
				return a.fail("%v", err)
			}
			fmt.Fprintf(a.out, "wrote %d man pages under %s\n", n, args[0])
			return nil
		},
	}
}

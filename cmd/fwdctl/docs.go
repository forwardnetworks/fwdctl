package main

import (
	"embed"
	"fmt"
	"io"
	"sort"
	"strings"
)

//go:embed guide/*.md
var guide embed.FS

// docsCmd prints the built-in guide: the binary documents its own standalone use, with no network and no repository.
func docsCmd(args []string, stdout, stderr io.Writer) int {
	entries, _ := guide.ReadDir("guide")
	var topics []string
	for _, e := range entries {
		topics = append(topics, strings.TrimSuffix(e.Name(), ".md"))
	}
	sort.Strings(topics)
	if len(args) == 0 {
		b, _ := guide.ReadFile("guide/overview.md")
		fmt.Fprintf(stdout, "%s\nMore: fwdctl docs <topic>, where topic is one of: %s\n", b, strings.Join(topics, ", "))
		return 0
	}
	if len(args) > 1 {
		fmt.Fprintln(stderr, "usage: fwdctl docs [topic]")
		return usage
	}
	b, err := guide.ReadFile("guide/" + args[0] + ".md")
	if err != nil {
		fmt.Fprintf(stderr, "error: no topic %q; topics: %s\n", args[0], strings.Join(topics, ", "))
		return usage
	}
	fmt.Fprintf(stdout, "%s", b)
	return 0
}

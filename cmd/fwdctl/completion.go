package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/skills"
)

// completionCommands are the commands a shell offers first; their sub-words follow.
var completionCommands = []string{"list", "describe", "run", "context", "nqe", "install", "docs", "update", "which", "login", "whoami", "redact-check", "dogfood-note", "completion", "version", "help"}

var completionSub = map[string][]string{
	"nqe":        {"lint", "fmt", "lsp", "complete", "hover"},
	"context":    {"nqe", "schema"},
	"install":    {"claude", "agents"},
	"docs":       {"overview", "skills", "nqe", "install", "troubleshooting"},
	"completion": {"bash", "zsh", "fish", "powershell"},
}

// completionCmd prints a completion script for a shell: fwdctl completion bash|zsh|fish|powershell. Skill names are the ones this binary has.
func completionCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: fwdctl completion bash|zsh|fish|powershell")
		return usage
	}
	names := skills.Names()
	sort.Strings(names)
	cmds := strings.Join(completionCommands, " ")
	sk := strings.Join(names, " ")
	subs := func(sep string) string {
		var keys []string
		for k := range completionSub {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s) words=\"%s\";;", k, strings.Join(completionSub[k], sep)))
		}
		return strings.Join(parts, "\n      ")
	}
	switch args[0] {
	case "bash":
		fmt.Fprintf(stdout, `# fwdctl bash completion: source <(fwdctl completion bash)
_fwdctl() {
  local cur=${COMP_WORDS[COMP_CWORD]} prev=${COMP_WORDS[COMP_CWORD-1]} words=""
  if [ "$COMP_CWORD" -eq 1 ]; then
    words="%s"
  elif [ "$COMP_CWORD" -eq 2 ]; then
    case "$prev" in
      run|describe) words="%s";;
      %s
    esac
  fi
  COMPREPLY=($(compgen -W "$words" -- "$cur"))
}
complete -F _fwdctl fwdctl
`, cmds, sk, subs(" "))
	case "zsh":
		fmt.Fprintf(stdout, `#compdef fwdctl
# fwdctl zsh completion: source <(fwdctl completion zsh)
_fwdctl() {
  local -a words
  if (( CURRENT == 2 )); then
    words=(%s)
  elif (( CURRENT == 3 )); then
    case $words[2] in
      run|describe) words=(%s);;
      %s
    esac
  fi
  compadd -- $words
}
compdef _fwdctl fwdctl
`, cmds, sk, subs(" "))
	case "fish":
		fmt.Fprintln(stdout, "# fwdctl fish completion: fwdctl completion fish | source")
		fmt.Fprintf(stdout, "complete -c fwdctl -f -n '__fish_use_subcommand' -a '%s'\n", cmds)
		fmt.Fprintf(stdout, "complete -c fwdctl -f -n '__fish_seen_subcommand_from run describe' -a '%s'\n", sk)
		var keys []string
		for k := range completionSub {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(stdout, "complete -c fwdctl -f -n '__fish_seen_subcommand_from %s' -a '%s'\n", k, strings.Join(completionSub[k], " "))
		}
	case "powershell":
		q := func(ws []string) string { return "'" + strings.Join(ws, "','") + "'" }
		fmt.Fprintln(stdout, "# fwdctl PowerShell completion: fwdctl completion powershell | Out-String | Invoke-Expression")
		fmt.Fprintf(stdout, `Register-ArgumentCompleter -Native -CommandName fwdctl -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)
  $words = $commandAst.CommandElements | ForEach-Object { $_.ToString() }
  $skills = @(%s)
  $sub = @{
`, q(names))
		var keys []string
		for k := range completionSub {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(stdout, "    '%s' = @(%s)\n", k, q(completionSub[k]))
		}
		fmt.Fprintf(stdout, `  }
  $choices = @()
  if ($words.Count -le 1 -or ($words.Count -eq 2 -and $wordToComplete)) { $choices = @(%s) }
  elseif ($words[1] -in 'run','describe') { $choices = $skills }
  elseif ($sub.ContainsKey($words[1])) { $choices = $sub[$words[1]] }
  $choices | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_) }
}
`, q(completionCommands))
	default:
		fmt.Fprintln(stderr, "usage: fwdctl completion bash|zsh|fish|powershell")
		return usage
	}
	return 0
}

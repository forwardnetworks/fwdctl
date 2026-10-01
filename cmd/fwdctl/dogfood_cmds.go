package main

import (
	"time"

	"github.com/spf13/cobra"
)

// DOGFOOD-TEMP: the two commands below go with the dogfood issue reporting (docs/internal.md, "Removing the dogfood issue reporting"). They keep their own argument parsing
// (cobra passes the arguments through) because they are removed at release.

const redactHelpBody = `Offline scan of a draft GitHub issue for customer data: IP addresses (except documentation ranges), hostnames, device-style names, URLs, secrets, emails, network/snapshot/org ids, home paths, and the words from your saved login (URL host, username) plus --deny and FWDCTL_REDACT_DENY. Prints JSON {ok, findings, note} with every excerpt masked; exit 0 only when clean, 1 on findings, 2 on warnings only (a possible customer or place name), 64 on bad usage. --deny repeats and takes comma lists; acme-corp, "acme corp" and AcmeCorp match one another. Also denied automatically: the saved login, OS user, machine name, redact_deny in the config file, FWDCTL_REDACT_DENY. Pass every customer, organization and network name you know: the tool cannot. Clean is necessary, not sufficient.`

func isHelpArg(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}

func (a *app) redactCmd() *cobra.Command {
	return &cobra.Command{
		Use: "redact-check [--file F | -] [--deny word]...", GroupID: "setup", Short: "DOGFOOD-TEMP: scan an issue draft for customer data before it is shown or filed",
		Long:               "usage: fwdctl redact-check [--file FILE | -] [--deny word]...   (DOGFOOD-TEMP)\n" + redactHelpBody,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if isHelpArg(args) {
				return cmd.Help()
			}
			return a.exit(redactCheckCmd(args, a.in, a.out, a.err, defaultConfigPath()))
		},
	}
}

func (a *app) noteCmd() *cobra.Command {
	return &cobra.Command{
		Use: "dogfood-note --ref SLUG [--file F | -]", GroupID: "setup", Short: "DOGFOOD-TEMP: save full private reproduction details to a local 0600 file (never uploaded)",
		Long:               "Save the full, private reproduction details of a dogfooding finding to a local file (mode 0600, never uploaded) and print its path. DOGFOOD-TEMP.",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if isHelpArg(args) {
				return cmd.Help()
			}
			return a.exit(dogfoodNoteCmd(args, a.in, a.out, a.err, dogfoodNoteDir(), time.Now()))
		},
	}
}

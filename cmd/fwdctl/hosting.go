package main

import (
	"os"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// The switches for running fwdctl inside a service on someone else's behalf (a hosted page, a Lambda). Each is an environment variable so the
// host sets it once for every child process:
//
//	FWDCTL_ALLOW_NETWORKS=ID[,ID...]  every request must be scoped to one of these networks (or a snapshot of one), checked before it is sent;
//	                                  routes not scoped to a network are refused and the network list holds only these
//	FWDCTL_READONLY=1                 PUT, PATCH and DELETE are refused at the transport and any edit- name with "apply": true is refused (a dry run still answers)
//	FWDCTL_QUIET=1                    nothing on stderr but errors: no "against URL as USER" line, no progress, heartbeat or dry-run note
//	FWDCTL_NO_STATE=1                 nothing read from or written to the user's config or cache: no saved login or profile, no daily update check, no auto-update
//	                                  (credentials then come only from FORWARD_URL, FORWARD_USERNAME, FORWARD_PASSWORD and the flags)

// guardFromEnv sets the session guards from FWDCTL_READONLY and FWDCTL_ALLOW_NETWORKS.
func guardFromEnv(cfg *fwd.Config) {
	if envOn("FWDCTL_READONLY") {
		cfg.ReadOnly = true
	}
	if v, ok := os.LookupEnv("FWDCTL_ALLOW_NETWORKS"); ok {
		cfg.AllowNetworks = strings.Split(v, ",") // set but empty is [""]: NewSession refuses it rather than allowing every network
	}
}

func envOn(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// quietEnv is FWDCTL_QUIET: only errors go to stderr.
func quietEnv() bool { return envOn("FWDCTL_QUIET") }

// noStateEnv is FWDCTL_NO_STATE: no config, profile or cache is read or written.
func noStateEnv() bool { return envOn("FWDCTL_NO_STATE") }

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// completeNetworks offers the networks the login can see ("id<TAB>name"), so `--network <TAB>` works. It needs a working login and quietly offers nothing without one: a completion
// must never print an error into the shell.
func (a *app) completeNetworks(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	sess := a.completionSession()
	if sess == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	nets, err := sess.Networks(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, n := range nets {
		if strings.HasPrefix(string(n.ID), toComplete) {
			out = append(out, fmt.Sprintf("%s\t%s", n.ID, n.Name))
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeSnapshots offers the snapshots of the network already given with --network ("id<TAB>state kind").
func (a *app) completeSnapshots(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	network, _ := cmd.Flags().GetString("network")
	if network == "" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	sess := a.completionSession()
	if sess == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	snaps, err := sess.Snapshots(ctx, network)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, sn := range snaps {
		if strings.HasPrefix(string(sn.ID), toComplete) {
			out = append(out, fmt.Sprintf("%s\t%s %s", sn.ID, sn.State, strings.ToLower(sn.ProcessingTrigger)))
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completionSession applies the connection (flags, environment, saved login) and opens a session, or returns nil.
func (a *app) completionSession() *fwd.Session {
	if err := applyConnectionOptions(a.conn); err != nil {
		return nil
	}
	s, err := a.session()
	if err != nil || s == nil {
		return nil
	}
	return s
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
	"github.com/forwardnetworks/fwdctl/skills"
)

const maxInventoryItems = 5000

// A source inventory is one JSON file naming everything a network collects: endpoint profiles, credentials (their secrets only by environment variable or file name), devices and endpoints, each
// referring to credentials and profiles by name. `source apply` reads what Forward holds, decides create / update / unchanged for every item by name, and runs each change through the same
// edit-platform / edit-source skill a person would, so every guard of those skills applies.
type inventorySpec struct {
	NetworkID   string           `json:"network_id"`
	Profiles    []map[string]any `json:"endpoint_profiles"`
	Credentials []map[string]any `json:"credentials"`
	Devices     []map[string]any `json:"devices"`
	Endpoints   []map[string]any `json:"endpoints"`
}

type applyRow struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Action string `json:"action"` // create, update, unchanged, failed, skipped
	Note   string `json:"note,omitempty"`
}

func (a *app) sourceCmd() *cobra.Command {
	c := parent(&cobra.Command{
		Use: "source", GroupID: "skills", Short: "set up a network's collection sources from one inventory file",
		Example: "  fwdctl source apply lab.json            # dry run: what would be created or updated\n  fwdctl source apply lab.json --apply",
		Long:    "Works on a whole source inventory at once instead of one edit-source call per item.",
	})
	var network string
	var apply bool
	ap := &cobra.Command{
		Use: "apply FILE [--network ID] [--apply]", Short: "create or update the profiles, credentials, devices and endpoints in an inventory file",
		Long: "FILE is JSON: {\"network_id\", \"endpoint_profiles\":[{name, command_sets, custom_commands}], \"credentials\":[{name, type, kind, username, secret_env or secret_file}], " +
			"\"devices\":[{name, host, type, port, credential, enable_credential, expert_credential, http_credential, snmp_credential, jump_server, collector_id, collect, note, enableSnmpCollection, collectBgpAdvertisements, bgpTableType, bgpPeerType, bgpAndBmpFailureHandling, disableIpv6Collection, fullCollectionLog}], " +
			"\"endpoints\":[{name, type, host, port, protocol, credential, profile, jump_server, collect, collector_id}]}. Credentials and profiles are referred to by NAME and resolved to ids. " +
			"A secret is never in the file: a credential names the environment variable (secret_env) or the mode-600 file (secret_file) that holds it.\n" +
			"Order: profiles, credentials, devices, endpoints. Each item is compared by name with what Forward holds: create, update, or unchanged. A dry run (the default) changes nothing. With --apply it stops at the first failure and marks the rest skipped, so a partial run is easy to read; run it again after fixing it, because unchanged items are left alone. " +
			"Secrets are never compared, so a changed password is not detected (rotate with edit-source). A device's enable credential and collection options are compared with what Forward reports.\n" +
			"The last line on stdout is JSON: status, mode, counts, items. Exit 0: done; 1: an item failed; 64: bad file; 3: Forward could not be read.",
		Example: "  fwdctl source apply lab.json --network 252606            # dry run: create / update / unchanged per item\n  LAB_PW=... fwdctl source apply lab.json --network 252606 --apply",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := os.ReadFile(args[0])
			if err != nil {
				return a.fail("%v", err)
			}
			sess, err := a.session()
			if err != nil {
				return a.fail("%v", err)
			}
			return a.exit(sourceApply(cmd.Context(), sess, raw, network, apply, a.out, a.err))
		},
	}
	ap.Flags().StringVar(&network, "network", "", "network id (overrides network_id in the file)")
	ap.Flags().BoolVar(&apply, "apply", false, "make the changes (default: a dry run)")
	c.AddCommand(ap)
	return c
}

func sourceApply(ctx context.Context, s *fwd.Session, raw []byte, network string, apply bool, stdout, stderr io.Writer) int {
	var spec inventorySpec
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		fmt.Fprintf(stderr, "error: the inventory is not valid: %v\n", err)
		return 64
	}
	if network != "" {
		spec.NetworkID = network
	}
	if spec.NetworkID == "" {
		fmt.Fprintln(stderr, "error: the inventory needs network_id (or --network)")
		return 64
	}
	if n := len(spec.Profiles) + len(spec.Credentials) + len(spec.Devices) + len(spec.Endpoints); n == 0 || n > maxInventoryItems {
		fmt.Fprintf(stderr, "error: the inventory holds %d items; it must hold between 1 and %d\n", n, maxInventoryItems)
		return 64
	}
	for _, group := range [][]map[string]any{spec.Profiles, spec.Credentials, spec.Devices, spec.Endpoints} {
		for _, it := range group {
			for k := range it {
				if fwd.SecretKey(k) && k != "secret_env" && k != "secret_file" {
					fmt.Fprintf(stderr, "error: item %v has a secret-looking field %q: a secret is never in the file; a credential names secret_env or secret_file\n", it["name"], k)
					return 64
				}
			}
			if name, _ := it["name"].(string); strings.TrimSpace(name) == "" {
				fmt.Fprintln(stderr, "error: every item needs a name")
				return 64
			}
		}
	}
	e := &applier{ctx: ctx, s: s, net: spec.NetworkID, apply: apply}
	if err := e.load(); err != nil {
		fmt.Fprintf(stderr, "error: Forward could not be read: %v\n", err)
		return 3
	}
	for _, it := range spec.Profiles {
		e.profileItem(it)
	}
	for _, it := range spec.Credentials {
		e.credential(it)
	}
	for _, it := range spec.Devices {
		e.device(it)
	}
	for _, it := range spec.Endpoints {
		e.endpoint(it)
	}
	counts := map[string]int{}
	for _, r := range e.rows {
		counts[r.Action]++
	}
	mode, status := "dry_run", "ok"
	if apply {
		mode = "applied"
	}
	if counts["failed"] > 0 {
		status = "failed"
	}
	for _, r := range e.rows {
		fmt.Fprintf(stderr, "%-9s %-10s %s%s\n", r.Action, r.Kind, r.Name, map[bool]string{true: "  (" + r.Note + ")", false: ""}[r.Note != ""])
	}
	b, _ := json.Marshal(map[string]any{"status": status, "mode": mode, "network_id": spec.NetworkID, "counts": counts, "items": e.rows})
	fmt.Fprintln(stdout, string(b))
	if status == "failed" {
		return 1
	}
	return 0
}

type applier struct {
	ctx   context.Context
	s     *fwd.Session
	net   string
	apply bool
	rows  []applyRow
	halt  bool
	// current state, by name
	cliCred, httpCred, snmpCred map[string]string
	profileID                   map[string]string
	devices                     map[string]map[string]any
	endpoints                   map[string]map[string]any
}

func (e *applier) load() error {
	e.cliCred, e.httpCred, e.snmpCred, e.profileID = map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}
	e.devices, e.endpoints = map[string]map[string]any{}, map[string]map[string]any{}
	cli, _, err := e.s.Client.Credentials.ListCLI(e.ctx, e.net)
	if err != nil {
		return err
	}
	for _, c := range cli {
		e.cliCred[c.Name] = c.ID
	}
	http, _, err := e.s.Client.Credentials.ListHTTP(e.ctx, e.net)
	if err != nil {
		return err
	}
	for _, c := range http {
		e.httpCred[c.Name] = c.ID
	}
	snmp, _, err := e.s.Client.Credentials.ListSNMP(e.ctx, e.net)
	if err != nil {
		return err
	}
	for _, c := range snmp {
		e.snmpCred[c.Name] = c.ID
	}
	return e.reload()
}

// reload reads the lists that change while an apply runs (profiles, devices, endpoints) and the credentials, so ids made earlier in the run resolve.
func (e *applier) reload() error {
	profiles, err := e.s.EndpointProfiles(e.ctx)
	if err != nil {
		return err
	}
	for _, p := range profiles {
		e.profileID[strings.ToLower(p.Name)] = string(p.ID)
	}
	devs, _, err := e.s.Client.ClassicDevices.List(e.ctx, e.net)
	if err != nil {
		return err
	}
	for _, d := range devs {
		g, _ := fwd.Generic(d)
		if m, ok := g.(map[string]any); ok {
			e.devices[d.Name] = m
		}
	}
	eps, err := e.s.Endpoints(e.ctx, e.net)
	if err != nil {
		return err
	}
	for _, p := range eps {
		g, _ := fwd.Generic(p)
		if m, ok := g.(map[string]any); ok {
			e.endpoints[p.Name] = m
		}
	}
	return nil
}

func (e *applier) add(kind, name, action, note string) {
	e.rows = append(e.rows, applyRow{kind, name, action, note})
}

// run sends one change through a skill; in a dry run it asks for the plan only. It returns whether it worked, and the reason when not.
func (e *applier) run(skill string, in map[string]any) (bool, string) {
	in["apply"] = e.apply
	raw, _ := json.Marshal(in)
	r, err := skills.Run(e.ctx, skill, e.s, raw)
	if err != nil {
		return false, err.Error()
	}
	if r.Status != result.OK {
		return false, string(r.Status) + ": " + r.Finding
	}
	return true, ""
}

// do records the outcome of a change: after a failure the rest are skipped.
func (e *applier) do(kind, name, action, skill string, in map[string]any, note ...string) {
	if e.halt {
		e.add(kind, name, "skipped", "an earlier item failed")
		return
	}
	ok, why := e.run(skill, in)
	if !ok {
		if e.apply {
			e.halt = true
		}
		e.add(kind, name, "failed", why+" "+strings.Join(note, ""))
		return
	}
	e.add(kind, name, action, strings.Join(note, ""))
	if e.apply {
		_ = e.reload()
		e.refreshCreds()
	}
}

func (e *applier) refreshCreds() {
	if cli, _, err := e.s.Client.Credentials.ListCLI(e.ctx, e.net); err == nil {
		for _, c := range cli {
			e.cliCred[c.Name] = c.ID
		}
	}
	if h, _, err := e.s.Client.Credentials.ListHTTP(e.ctx, e.net); err == nil {
		for _, c := range h {
			e.httpCred[c.Name] = c.ID
		}
	}
	if sn, _, err := e.s.Client.Credentials.ListSNMP(e.ctx, e.net); err == nil {
		for _, c := range sn {
			e.snmpCred[c.Name] = c.ID
		}
	}
}

// ref turns a name into an id from table, or, in a dry run only, a placeholder for something this same run creates.
func (e *applier) ref(table map[string]string, kind, name string) (string, error) {
	if name == "" {
		return "", nil
	}
	if id, ok := table[name]; ok {
		return id, nil
	}
	if !e.apply {
		return "<new " + kind + " " + name + ">", nil
	}
	return "", fmt.Errorf("%s %q is not in Forward and not created earlier in this inventory", kind, name)
}

func (e *applier) profileRef(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	if id, ok := e.profileID[strings.ToLower(name)]; ok {
		return id, nil
	}
	if !e.apply {
		return "<new profile " + name + ">", nil
	}
	return "", fmt.Errorf("endpoint profile %q is not in Forward and not created earlier in this inventory", name)
}

func (e *applier) profileItem(it map[string]any) {
	name := it["name"].(string)
	if e.halt {
		e.add("profile", name, "skipped", "an earlier item failed")
		return
	}
	if _, ok := e.profileID[strings.ToLower(name)]; ok {
		e.add("profile", name, "unchanged", "exists; its commands are not compared")
		return
	}
	def := map[string]any{"name": name}
	for k, v := range it {
		if k != "name" {
			def[k] = v
		}
	}
	e.do("profile", name, "create", "edit-platform", map[string]any{"area": "endpoint_profiles", "action": "create", "definition": def})
}

func (e *applier) credential(it map[string]any) {
	name := it["name"].(string)
	if e.halt {
		e.add("credential", name, "skipped", "an earlier item failed")
		return
	}
	typ := strings.ToUpper(fmt.Sprint(it["type"]))
	table := map[string]map[string]string{"CLI": e.cliCred, "HTTP": e.httpCred, "SNMP": e.snmpCred}[typ]
	if table == nil {
		e.add("credential", name, "failed", "type must be CLI, HTTP or SNMP")
		return
	}
	if _, ok := table[name]; ok {
		e.add("credential", name, "unchanged", "exists; secrets are never compared")
		return
	}
	in := map[string]any{"network_id": e.net, "object": "credential", "action": "create"}
	def := map[string]any{}
	for k, v := range it {
		switch k {
		case "secret_env", "secret_file":
			in[k] = v
		default:
			def[k] = v
		}
	}
	in["definition"] = def
	e.do("credential", name, "create", "edit-source", in)
}

// withRefs copies the item's fields into def, turning the name-valued references into ids.
func (e *applier) withRefs(it map[string]any, refs map[string]struct {
	to    string
	table map[string]string
	kind  string
}) (map[string]any, error) {
	def := map[string]any{}
	for k, v := range it {
		if r, ok := refs[k]; ok {
			id, err := e.ref(r.table, r.kind, fmt.Sprint(v))
			if err != nil {
				return nil, err
			}
			def[r.to] = id
			continue
		}
		def[k] = v
	}
	return def, nil
}

type refSpec = struct {
	to    string
	table map[string]string
	kind  string
}

func (e *applier) device(it map[string]any) {
	name := it["name"].(string)
	if e.halt {
		e.add("device", name, "skipped", "an earlier item failed")
		return
	}
	def, err := e.withRefs(it, map[string]refSpec{
		"credential":        {"cliCredentialId", e.cliCred, "credential"},
		"enable_credential": {"cliCredential2Id", e.cliCred, "credential"},
		"expert_credential": {"cliCredential3Id", e.cliCred, "credential"},
		"http_credential":   {"httpCredentialId", e.httpCred, "credential"},
		"snmp_credential":   {"snmpCredentialId", e.snmpCred, "credential"},
		"jump_server":       {"jumpServerId", map[string]string{}, "jump server"},
		"collector_id":      {"collectorId", map[string]string{}, "collector"},
	})
	if err != nil {
		e.add("device", name, "failed", err.Error())
		return
	}
	// jump_server and collector_id are ids already, not names
	if v, ok := it["jump_server"]; ok {
		delete(def, "jumpServerId")
		def["jumpServerId"] = v
	}
	if v, ok := it["collector_id"]; ok {
		delete(def, "collectorId")
		def["collectorId"] = v
	}
	cur, exists := e.devices[name]
	if !exists {
		e.do("device", name, "create", "edit-source", map[string]any{"network_id": e.net, "object": "classic_device", "action": "create", "definition": def})
		return
	}
	diff := map[string]any{}
	for k, v := range def {
		if k == "name" {
			continue
		}
		have, present := cur[k]
		if !present && (k == "bgpTableType" || k == "bgpPeerType") {
			have, present = "BOTH", true // Forward leaves these out of its answer when they are BOTH
		}
		if !present && fmt.Sprint(v) == "false" {
			continue // an option left out of the answer is off
		}
		if fmt.Sprint(have) != fmt.Sprint(v) {
			diff[k] = v
		}
	}
	if len(diff) == 0 {
		e.add("device", name, "unchanged", "host, credentials and collection options match what Forward holds")
		return
	}
	e.do("device", name, "update", "edit-source", map[string]any{"network_id": e.net, "object": "classic_device", "action": "update", "name": name, "definition": diff}, "differs: "+keysOf(diff))
}

func (e *applier) endpoint(it map[string]any) {
	name := it["name"].(string)
	if e.halt {
		e.add("endpoint", name, "skipped", "an earlier item failed")
		return
	}
	prof := ""
	def := map[string]any{}
	for k, v := range it {
		switch k {
		case "credential":
			id, err := e.ref(e.cliCred, "credential", fmt.Sprint(v))
			if err != nil {
				e.add("endpoint", name, "failed", err.Error())
				return
			}
			def["credentialId"] = id
		case "profile":
			prof = fmt.Sprint(v)
			id, err := e.profileRef(prof)
			if err != nil {
				e.add("endpoint", name, "failed", err.Error())
				return
			}
			def["profileId"] = id
		case "jump_server":
			def["jumpServerId"] = v
		case "collector_id":
			def["collectorId"] = v
		default:
			def[k] = v
		}
	}
	cur, exists := e.endpoints[name]
	if !exists {
		e.do("endpoint", name, "create", "edit-source", map[string]any{"network_id": e.net, "object": "endpoint", "action": "create", "definition": def})
		return
	}
	diff := map[string]any{}
	for _, k := range []string{"host", "credentialId", "profileId", "protocol", "jumpServerId", "collectorId", "collect"} {
		if v, want := def[k]; want && fmt.Sprint(cur[k]) != fmt.Sprint(v) {
			diff[k] = v
		}
	}
	if len(diff) == 0 {
		e.add("endpoint", name, "unchanged", "")
		return
	}
	e.do("endpoint", name, "update", "edit-source", map[string]any{"network_id": e.net, "object": "endpoint", "action": "update", "name": name, "definition": diff}, "differs: "+keysOf(diff))
}

func keysOf(m map[string]any) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ", ")
}

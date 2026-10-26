package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// cloudDefinitionFields are the definition fields each cloud_account action uses. A field outside its action's list is refused by name: decoding into one shared struct
// would otherwise accept it and the action would ignore it, reporting success for a change that did not happen.
var cloudDefinitionFields = map[string][]string{
	"create": {"type", "name", "collect", "proxyServerId", "regions", "username", "clientId", "clientEmail", "privateKeyId", "tenant", "environment", "subscriptionIds", "concurrency"},
	"rotate": {"type", "name", "username", "clientId", "clientEmail", "privateKeyId", "tenant"},
	"update": {"collect", "accounts_enabled", "c2s_accounts", "accounts_external_ids", "accounts_add", "add_newly_discovered"},
}

// refuseUnusedCloudFields returns an invalid-input error naming any definition field the action does not use.
func refuseUnusedCloudFields(raw json.RawMessage, action string) error {
	if len(raw) == 0 {
		return nil
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil // decodeDefinition reports a malformed definition
	}
	allowed := map[string]bool{}
	for _, f := range cloudDefinitionFields[action] {
		allowed[f] = true
	}
	var extra []string
	for k := range keys {
		if !allowed[k] {
			extra = append(extra, k)
		}
	}
	if len(extra) == 0 {
		return nil
	}
	sort.Strings(extra)
	return fmt.Errorf("%w: cloud_account %s does not use definition field(s) %s (it takes: %s); a field it would ignore is refused so a change that did not happen is never reported as done",
		ErrInvalidInput, action, strings.Join(extra, ", "), strings.Join(cloudDefinitionFields[action], ", "))
}

func planCloudAccount(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	cur, _, err := s.Client.CloudAccounts.List(ctx, in.NetworkID)
	if err != nil {
		return nil, err
	}
	find := func(name string) *forward.CloudAccount {
		for i := range cur {
			if cur[i].Name == name {
				return &cur[i]
			}
		}
		return nil
	}
	var def struct {
		Type            string         `json:"type"`
		Name            string         `json:"name"`
		Collect         *bool          `json:"collect"`
		ProxyServerID   string         `json:"proxyServerId"`
		Regions         map[string]int `json:"regions"`
		Username        string         `json:"username"`
		ClientID        string         `json:"clientId"`
		ClientEmail     string         `json:"clientEmail"`
		PrivateKeyID    string         `json:"privateKeyId"`
		Tenant          string         `json:"tenant"`
		Environment     string         `json:"environment"`
		SubscriptionIDs []string       `json:"subscriptionIds"`
		Concurrency     *int64         `json:"concurrency"`
		// AccountsEnabled turns discovered member accounts (AWS assume-role) on or off by account id; update only
		AccountsEnabled map[string]bool `json:"accounts_enabled"`
		// C2SAccounts is a C2S Access Portal (CAP) account's portal account list; update only. A pointer: an empty list is refused (see the plan) but is not the same as absent) is not the same as absent.
		C2SAccounts *[]struct {
			AccountID string `json:"account_id"`
			Role      string `json:"role"`
		} `json:"c2s_accounts"`
	}
	fields := "type (AWS, AZURE, GCP, ...), name, collect, proxyServerId, regions, username (an AWS access key id), clientId, clientEmail, privateKeyId, tenant, environment, subscriptionIds, concurrency, accounts_enabled and c2s_accounts (update only)"
	// the credential half: the secret names each provider needs, read from the secret file
	credentialOf := func(secs map[string]fwd.Secret) forward.CloudAccountCredentialRequest {
		return forward.CloudAccountCredentialRequest{Type: strings.ToUpper(def.Type), Username: def.Username, Password: secs["password"].Reveal(), ClientID: def.ClientID, Tenant: def.Tenant,
			ClientEmail: def.ClientEmail, PrivateKeyID: def.PrivateKeyID, PrivateKey: secs["privateKey"].Reveal(), APIKey: secs["apiKey"].Reveal()}
	}
	secretNames := []string{"password", "privateKey", "apiKey"}
	switch in.Action {
	case "delete":
		if in.Name == "" {
			return nil, fmt.Errorf("%w: delete needs name (the account)", ErrInvalidInput)
		}
		if err := noSecretInputs(in); err != nil {
			return nil, err
		}
		a := find(in.Name)
		if a == nil {
			return nil, nil
		}
		before, _ := fwd.Generic(a)
		fwd.RedactSecrets(before) // the account as read back carries assume-role external ids
		return &networkPlan{target: fmt.Sprintf("delete cloud account %q from network %s", in.Name, in.NetworkID), action: "delete_cloud_account", before: before, after: nil, reversible: false, confirm: in.Name,
			undo:   "none: the stored keys are gone; create the account again with its credentials",
			limits: []string{"its VPCs and instances disappear from the next snapshot; past snapshots keep them"},
			do: func(ctx context.Context) error {
				_, err := s.Client.CloudAccounts.Delete(ctx, in.NetworkID, in.Name)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				l, _, e := s.Client.CloudAccounts.List(ctx, in.NetworkID)
				for _, x := range l {
					if x.Name == in.Name {
						return false, nil, e
					}
				}
				return true, nil, e
			}}, nil
	case "test":
		if in.Name == "" || find(in.Name) == nil {
			return nil, nil
		}
		if err := noSecretInputs(in); err != nil {
			return nil, err
		}
		return &networkPlan{target: fmt.Sprintf("test the connection of cloud account %q on network %s", in.Name, in.NetworkID), action: "test_cloud_account", before: nil, after: "Forward records the result per region", reversible: true, undo: "none needed: a test changes only the recorded test result",
			limits: []string{"the test calls the cloud provider from the collector; read the result with inspect-collection view config"},
			do: func(ctx context.Context) error {
				_, err := s.Client.CloudAccounts.Test(ctx, in.NetworkID, in.Name)
				return err
			}}, nil
	case "update":
		if in.Name == "" {
			return nil, fmt.Errorf("%w: update needs name (the account)", ErrInvalidInput)
		}
		memberEdit := hasMemberEdit(in.Definition)
		if !memberEdit {
			if err := noSecretInputs(in); err != nil {
				return nil, err
			}
		}
		a := find(in.Name)
		if a == nil {
			return nil, nil
		}
		if err := refuseUnusedCloudFields(in.Definition, "update"); err != nil {
			return nil, err
		}
		if memberEdit {
			return planMembers(ctx, s, in, a)
		}
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		isCAP := a.HasCAPCredential()
		if isCAP && len(def.AccountsEnabled) > 0 {
			return nil, fmt.Errorf("%w: %q is collected through a C2S Access Portal credential: Forward filters its accounts with the portal account list, the enabled flags in accounts_enabled are not used for it, so turning an account on or off there would change nothing that is collected. Set the portal account list with c2s_accounts (an empty list collects every account the portal returns), or switch the whole setup with collect", ErrInvalidInput, in.Name)
		}
		if def.C2SAccounts != nil && !isCAP {
			return nil, fmt.Errorf("%w: %q is not collected through a C2S Access Portal credential, so it has no portal account list; use accounts_enabled for its member accounts", ErrInvalidInput, in.Name)
		}
		if def.Collect == nil && len(def.AccountsEnabled) == 0 && def.C2SAccounts == nil {
			return nil, fmt.Errorf("%w: update needs definition.collect (true or false), definition.accounts_enabled (an account id mapped to true or false) or, for a C2S Access Portal setup, definition.c2s_accounts", ErrInvalidInput)
		}
		infos := append([]forward.AWSAssumeRoleInfo(nil), a.AssumeRoleInfos...)
		index := map[string]int{}
		for i, x := range infos {
			index[x.AccountID] = i
		}
		beforeAcc, afterAcc := map[string]bool{}, map[string]bool{}
		for id, on := range def.AccountsEnabled {
			i, ok := index[id]
			if !ok {
				known := make([]string, 0, len(infos))
				for _, x := range infos {
					known = append(known, x.AccountID)
				}
				sort.Strings(known)
				shown, _ := result.CapRow(known, 12)
				return nil, fmt.Errorf("%w: %q is not a member account of %q; its accounts are: %s", ErrInvalidInput, id, in.Name, strings.Join(shown, ", "))
			}
			beforeAcc[id], afterAcc[id] = infos[i].Enabled, on
			infos[i].Enabled = on
		}
		var wantC2S []forward.AWSC2SAccountInfo
		if def.C2SAccounts != nil {
			wantC2S = make([]forward.AWSC2SAccountInfo, 0, len(*def.C2SAccounts))
			for _, x := range *def.C2SAccounts {
				if x.AccountID == "" || x.Role == "" {
					return nil, fmt.Errorf("%w: each c2s_accounts entry needs account_id and role", ErrInvalidInput)
				}
				wantC2S = append(wantC2S, forward.AWSC2SAccountInfo{AccountID: x.AccountID, AccountRole: x.Role})
			}
		}
		if def.C2SAccounts != nil && len(wantC2S) == 0 {
			return nil, fmt.Errorf("%w: c2s_accounts is empty. For a C2S Access Portal setup Forward's collection step requires at least one account and rejects an empty list before it asks the portal, so the AWS snapshot would come back incomplete (read in Forward's source, not run live). The setup test and discovery treat an empty list as no filter, which is why an empty list can look fine in the UI. List the accounts to collect", ErrInvalidInput)
		}
		c2sView := func(l []forward.AWSC2SAccountInfo) []map[string]any {
			out := make([]map[string]any, 0, len(l))
			for _, x := range l {
				out = append(out, map[string]any{"account_id": x.AccountID, "role": x.AccountRole})
			}
			return out
		}
		before := map[string]any{"collect": a.Collect}
		after := map[string]any{"collect": a.Collect}
		if def.Collect != nil {
			after["collect"] = *def.Collect
		}
		if len(def.AccountsEnabled) > 0 {
			before["accounts_enabled"], after["accounts_enabled"] = beforeAcc, afterAcc
		}
		if def.C2SAccounts != nil {
			before["c2s_accounts"], after["c2s_accounts"] = c2sView(a.AWSC2SAccountInfos), c2sView(wantC2S)
		}
		patch := forward.CloudAccountPatch{Type: a.Type, Collect: def.Collect}
		if len(def.AccountsEnabled) > 0 {
			patch.AssumeRoleInfos = &infos
		}
		if def.C2SAccounts != nil {
			patch.AWSC2SAccountInfos = &wantC2S
		}
		limits := []string{"an account's errorMsg is Forward's own record of its last attempt; this does not clear it, and the next collection or a test (action test) refreshes it",
			"this sets what the next collection will use; it does not start one (edit-collection)"}
		if len(def.AccountsEnabled) > 0 {
			limits = append([]string{"the account list is replaced as a whole: every other account is sent unchanged, with its current enabled flag"}, limits...)
		}
		if def.C2SAccounts != nil {
			have := map[[2]string]bool{}
			for _, x := range a.AWSC2SAccountInfos {
				have[[2]string{x.AccountID, x.AccountRole}] = true
			}
			var fresh []string
			for _, w := range wantC2S {
				if !have[[2]string{w.AccountID, w.AccountRole}] {
					fresh = append(fresh, "("+w.AccountID+", "+w.AccountRole+")")
				}
			}
			if len(fresh) > 0 {
				limits = append([]string{"these (account id, role) pairs are not in the account's current list and are not checked against the access portal: " + strings.Join(fresh, ", ") +
					". A wrong account id or role makes the collection filter match nothing, and AWS then yields no data without an error (a thin snapshot, not a failure); confirm them against the accounts the portal offers (the setup's account discovery in the Forward UI) before applying"}, limits...)
			}
			limits = append([]string{"c2s_accounts replaces the whole portal account list: list every account that should be collected (an empty list is refused: Forward's collection step rejects it)"}, limits...)
		}
		return &networkPlan{target: fmt.Sprintf("update cloud account %q on network %s", in.Name, in.NetworkID), action: "update_cloud_account", before: before, after: after, reversible: true,
			undo:   "run update again with the before values (accounts_enabled, c2s_accounts and collect as shown in before)",
			limits: limits,
			do: func(ctx context.Context) error {
				_, _, err := s.Client.CloudAccounts.Patch(ctx, in.NetworkID, in.Name, patch)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				l, _, e := s.Client.CloudAccounts.List(ctx, in.NetworkID)
				for _, x := range l {
					if x.Name != in.Name {
						continue
					}
					got := map[string]bool{}
					for _, m := range x.AssumeRoleInfos {
						if _, asked := def.AccountsEnabled[m.AccountID]; asked {
							got[m.AccountID] = m.Enabled
						}
					}
					ok := def.Collect == nil || x.Collect == *def.Collect
					for id, on := range def.AccountsEnabled {
						if v, seen := got[id]; !seen || v != on {
							ok = false
						}
					}
					back := map[string]any{"collect": x.Collect}
					if len(def.AccountsEnabled) > 0 {
						back["accounts_enabled"] = got
					}
					if def.C2SAccounts != nil {
						have := map[string]string{}
						for _, c := range x.AWSC2SAccountInfos {
							have[c.AccountID] = c.AccountRole
						}
						if len(have) != len(wantC2S) {
							ok = false
						}
						for _, w := range wantC2S {
							if have[w.AccountID] != w.AccountRole {
								ok = false
							}
						}
						back["c2s_accounts"] = c2sView(x.AWSC2SAccountInfos)
					}
					return ok, back, e
				}
				return false, nil, e
			}}, nil
	case "create", "rotate":
		if err := refuseUnusedCloudFields(in.Definition, in.Action); err != nil {
			return nil, err
		}
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		kind := strings.ToUpper(def.Type)
		if kind == "" {
			return nil, fmt.Errorf("%w: definition.type is required (AWS, AZURE, GCP, ...)", ErrInvalidInput)
		}
		if in.Action == "create" {
			if def.Name == "" {
				return nil, fmt.Errorf("%w: create needs definition.name", ErrInvalidInput)
			}
			if find(def.Name) != nil {
				return nil, fmt.Errorf("%w: an account named %q already exists on the network", ErrInvalidInput, def.Name)
			}
		} else if in.Name == "" || find(in.Name) == nil {
			return nil, nil
		}
		name := firstNonEmpty(in.Name, def.Name)
		verb, act := "create", "create_cloud_account"
		undo := "delete the new account (edit-source object cloud_account action delete, confirm its name)"
		if in.Action == "rotate" {
			verb, act, undo = "replace the stored credentials of", "rotate_cloud_account_credentials", "none: the earlier secret is not kept by Forward; enter it again to go back"
		}
		plan := &networkPlan{target: fmt.Sprintf("%s cloud account %q (%s) on network %s", verb, name, kind, in.NetworkID), action: act, before: nil,
			after: map[string]any{"type": kind, "name": name, "collect": def.Collect, "regions": def.Regions, "secret": "<secret>"}, reversible: in.Action == "create", undo: undo,
			limits: []string{"the secret is read from " + secretSource(in) + " (plain text is the password/secret key; a JSON object may carry password, privateKey or apiKey) and is never shown, logged or returned", "the account is tested with action test; regions are collected from the next collection"}}
		if in.Action == "create" {
			collect := true
			if def.Collect != nil {
				collect = *def.Collect
			}
			if in.Apply {
				secs, err := sourceSecrets(in, secretNames...)
				if err != nil {
					return nil, err
				}
				cred := credentialOf(secs)
				req := forward.CloudAccountRequest{Type: kind, Name: def.Name, Collect: collect, ProxyServerID: def.ProxyServerID, Regions: def.Regions, Username: cred.Username, Password: cred.Password,
					ClientID: def.ClientID, Tenant: def.Tenant, Environment: def.Environment, SubscriptionIDs: def.SubscriptionIDs, Concurrency: def.Concurrency}
				if kind == "GCP" {
					req.Extra = map[string]any{"clientEmail": cred.ClientEmail, "privateKeyId": cred.PrivateKeyID, "privateKey": cred.PrivateKey}
				} else if cred.APIKey != "" {
					req.Extra = map[string]any{"apiKey": cred.APIKey}
				}
				plan.do = func(ctx context.Context) error {
					_, _, err := s.Client.CloudAccounts.Create(ctx, in.NetworkID, req)
					return err
				}
				plan.verify = func(ctx context.Context) (bool, any, error) {
					l, _, e := s.Client.CloudAccounts.List(ctx, in.NetworkID)
					for _, x := range l {
						if x.Name == def.Name {
							return true, nil, e
						}
					}
					return false, nil, e
				}
			}
			return plan, nil
		}
		if in.Apply {
			secs, err := sourceSecrets(in, secretNames...)
			if err != nil {
				return nil, err
			}
			cred := credentialOf(secs)
			plan.do = func(ctx context.Context) error {
				_, err := s.Client.CloudAccounts.UpdateCredential(ctx, in.NetworkID, name, cred)
				return err
			}
		}
		return plan, nil
	}
	return nil, fmt.Errorf("%w: object cloud_account takes action create, update, rotate, test or delete", ErrInvalidInput)
}

// planRapid7 creates or updates a Rapid7 vulnerability source of the network. Its login is a credential that already exists (credential_id), so no secret is read here.
func planRapid7(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	if err := noSecretInputs(in); err != nil {
		return nil, err
	}
	var def struct {
		Name                 string   `json:"name"`
		BaseURL              string   `json:"baseUrl"`
		CredentialID         string   `json:"credentialId"`
		DisableSSLValidation bool     `json:"disableSslValidation"`
		CollectionDisabled   bool     `json:"collectionDisabled"`
		ReportNames          []string `json:"reportNames"`
	}
	if in.Action != "delete" {
		if err := decodeDefinition(in.Definition, &def, "name, baseUrl, credentialId, disableSslValidation, collectionDisabled, reportNames"); err != nil {
			return nil, err
		}
	}
	cur, _, err := s.Client.Integrations.ListRapid7(ctx, in.NetworkID)
	if err != nil {
		return nil, err
	}
	req := forward.Rapid7SourceRequest{Name: def.Name, BaseURL: def.BaseURL, DisableSSLValidation: def.DisableSSLValidation, CredentialID: forward.Identifier(def.CredentialID),
		CollectionDisabled: def.CollectionDisabled, ReportNames: def.ReportNames}
	shown := func(name string) func(context.Context) (bool, any, error) {
		return func(ctx context.Context) (bool, any, error) {
			l, _, e := s.Client.Integrations.ListRapid7(ctx, in.NetworkID)
			for _, x := range l {
				if x.Name == name {
					g, _ := fwd.Generic(x)
					return x.BaseURL == def.BaseURL && x.CollectionDisabled == def.CollectionDisabled, g, e
				}
			}
			return false, nil, e
		}
	}
	switch in.Action {
	case "create":
		if def.Name == "" || def.BaseURL == "" || def.CredentialID == "" {
			return nil, fmt.Errorf("%w: create needs name, baseUrl and credentialId (a credential from inspect-platform area credentials)", ErrInvalidInput)
		}
		for _, x := range cur {
			if x.Name == def.Name {
				return nil, fmt.Errorf("%w: a Rapid7 source named %q already exists", ErrInvalidInput, def.Name)
			}
		}
		return &networkPlan{target: fmt.Sprintf("create Rapid7 source %q on network %s", def.Name, in.NetworkID), action: "create_rapid7_source", after: req, reversible: false,
			undo:   "none through the API: there is no Rapid7 source delete (disable it with update and collectionDisabled)",
			limits: []string{"this does not test that Rapid7 accepts the credential"},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.Integrations.CreateRapid7(ctx, in.NetworkID, req)
				return err
			},
			verify: shown(def.Name)}, nil
	case "update":
		for _, x := range cur {
			if x.Name != in.Name {
				continue
			}
			if def.Name == "" {
				req.Name = x.Name
				def.Name = x.Name
			}
			if def.BaseURL == "" || def.CredentialID == "" {
				return nil, fmt.Errorf("%w: update REPLACES the source: restate baseUrl and credentialId", ErrInvalidInput)
			}
			before, _ := fwd.Generic(x)
			return &networkPlan{target: fmt.Sprintf("update Rapid7 source %q on network %s", x.Name, in.NetworkID), action: "update_rapid7_source", before: before, after: req, reversible: true,
				undo: "update again with the before values",
				do: func(ctx context.Context) error {
					_, _, err := s.Client.Integrations.UpdateRapid7(ctx, in.NetworkID, in.Name, req)
					return err
				},
				verify: shown(req.Name)}, nil
		}
		return nil, nil
	case "delete":
		for _, x := range cur {
			if x.Name != in.Name {
				continue
			}
			before, _ := fwd.Generic(x)
			return &networkPlan{target: fmt.Sprintf("delete Rapid7 source %q from network %s", x.Name, in.NetworkID), action: "delete_rapid7_source", before: before, reversible: false, confirm: x.Name,
				undo: "none: create it again from the before values (the credential it used is not deleted)",
				do: func(ctx context.Context) error {
					_, err := s.Client.Integrations.DeleteRapid7(ctx, in.NetworkID, x.Name)
					return err
				},
				verify: func(ctx context.Context) (bool, any, error) {
					l, _, e := s.Client.Integrations.ListRapid7(ctx, in.NetworkID)
					for _, y := range l {
						if y.Name == x.Name {
							return false, y, e
						}
					}
					return true, nil, e
				}}, nil
		}
		return nil, nil
	}
	return nil, fmt.Errorf("%w: object rapid7_source takes action create, update or delete (name is the source to update or delete)", ErrInvalidInput)
}

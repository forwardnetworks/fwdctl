package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

var accountIDRe = regexp.MustCompile(`^\d{12}$`)

// memberDef is the member-account part of a cloud_account update: external ids set or cleared for listed accounts, newly discovered accounts appended, and enabled flags. It is a separate plan because the
// account list is replaced as a whole on the wire: every other entry is resent unchanged, and the account is read again right before the write.
type memberDef struct {
	ExternalIDs     []string        `json:"accounts_external_ids"`
	Add             []string        `json:"accounts_add"`
	AddNewly        bool            `json:"add_newly_discovered"`
	AccountsEnabled map[string]bool `json:"accounts_enabled"`
}

func hasMemberEdit(raw json.RawMessage) bool {
	var keys map[string]json.RawMessage
	if json.Unmarshal(raw, &keys) != nil {
		return false
	}
	for _, k := range []string{"accounts_external_ids", "accounts_add", "add_newly_discovered"} {
		if _, ok := keys[k]; ok {
			return true
		}
	}
	return false
}

// roleNameOf is the role a setup's member accounts share: the part after "role/" of every entry's role ARN. A setup whose entries disagree has no single role to give a new account.
func roleNameOf(infos []forward.AWSAssumeRoleInfo) (string, error) {
	names := map[string]bool{}
	for _, x := range infos {
		if i := strings.Index(x.RoleARN, ":role/"); i >= 0 {
			names[x.RoleARN[i+len(":role/"):]] = true
		}
	}
	if len(names) != 1 {
		return "", fmt.Errorf("the setup's member accounts use %d different role names, so a new account has no single role to take", len(names))
	}
	for n := range names {
		return n, nil
	}
	return "", nil
}

func planMembers(ctx context.Context, s *fwd.Session, in editSourceInput, a *forward.CloudAccount) (*networkPlan, error) {
	var def memberDef
	var all map[string]json.RawMessage
	_ = json.Unmarshal(in.Definition, &all)
	for k := range all {
		if k != "accounts_external_ids" && k != "accounts_add" && k != "add_newly_discovered" && k != "accounts_enabled" {
			return nil, fmt.Errorf("%w: accounts_external_ids, accounts_add and add_newly_discovered are changed on their own (with accounts_enabled at most); %q is a separate update", ErrInvalidInput, k)
		}
	}
	if err := decodeDefinition(in.Definition, &def, "accounts_external_ids (account ids; the values come from secret_file or secret_env as a JSON object {id: value|null}), accounts_add (account ids), add_newly_discovered, accounts_enabled"); err != nil {
		return nil, err
	}
	if a.HasCAPCredential() {
		return nil, fmt.Errorf("%w: %q is collected through a C2S Access Portal credential and has no assume-role member accounts to edit", ErrInvalidInput, in.Name)
	}
	if !strings.EqualFold(a.Type, "AWS") {
		return nil, fmt.Errorf("%w: %q is a %s setup; member account edits are for AWS multi-account setups", ErrInvalidInput, in.Name, a.Type)
	}
	dup := map[string]bool{}
	for _, id := range append(append([]string{}, def.ExternalIDs...), def.Add...) {
		if !accountIDRe.MatchString(id) {
			return nil, fmt.Errorf("%w: %q is not an AWS account id (12 digits)", ErrInvalidInput, id)
		}
	}
	for _, list := range [][]string{def.ExternalIDs, def.Add} {
		seen := map[string]bool{}
		for _, id := range list {
			if seen[id] {
				return nil, fmt.Errorf("%w: account %s is listed twice", ErrInvalidInput, id)
			}
			seen[id] = true
		}
	}
	_ = dup
	// the values: a JSON object {id: value|null} from the secret file or variable; never echoed. A dry run without one still checks everything else.
	var vals map[string]*string
	valuesRead := false
	if len(def.ExternalIDs) > 0 {
		if in.SecretFile == "" && in.SecretEnv == "" {
			if in.Apply {
				return nil, fmt.Errorf("%w: accounts_external_ids needs the values: secret_file (a mode-600 JSON file {\"<account id>\": \"<external id>\" or null to clear}) or secret_env", ErrInvalidInput)
			}
		} else {
			sec, err := fwd.ReadSecret(in.SecretFile, in.SecretEnv)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
			}
			if err := json.Unmarshal([]byte(strings.TrimSpace(sec.Reveal())), &vals); err != nil {
				return nil, fmt.Errorf("%w: the external id values must be a JSON object {\"<account id>\": \"<value>\" or null}: %v", ErrInvalidInput, err)
			}
			valuesRead = true
			listed := map[string]bool{}
			for _, id := range def.ExternalIDs {
				listed[id] = true
				if _, ok := vals[id]; !ok {
					return nil, fmt.Errorf("%w: account %s is listed in accounts_external_ids but the values file has no entry for it (use null to clear)", ErrInvalidInput, id)
				}
			}
			for id := range vals {
				if !listed[id] {
					return nil, fmt.Errorf("%w: the values file has an entry for %s, which accounts_external_ids does not list; only listed accounts are touched", ErrInvalidInput, id)
				}
			}
		}
	} else if in.SecretFile != "" || in.SecretEnv != "" {
		return nil, fmt.Errorf("%w: this update takes no secret unless accounts_external_ids is given", ErrInvalidInput)
	}
	if len(def.Add) > 0 && def.AddNewly {
		return nil, fmt.Errorf("%w: give accounts_add (specific ids) or add_newly_discovered, not both", ErrInvalidInput)
	}

	discovered := func(ctx context.Context) (map[string]string, error) {
		if len(def.Add) == 0 && !def.AddNewly {
			return nil, nil
		}
		d, _, err := s.Client.CloudAccounts.NewlyDiscovered(ctx, in.NetworkID)
		if err != nil {
			return nil, err
		}
		out := map[string]string{}
		for _, x := range d[in.Name] {
			out[x.ID] = x.Name
		}
		return out, nil
	}
	// compose builds the full member list from an account as it is NOW: the listed changes applied, everything else exactly as it was.
	compose := func(cur forward.CloudAccount, disc map[string]string) ([]forward.AWSAssumeRoleInfo, map[string]any, error) {
		infos := append([]forward.AWSAssumeRoleInfo(nil), cur.AssumeRoleInfos...)
		index := map[string]int{}
		for i, x := range infos {
			index[x.AccountID] = i
		}
		summary := map[string]any{}
		var changedIDs []string
		for _, id := range def.ExternalIDs {
			i, ok := index[id]
			if !ok && !inList(def.Add, id) && !(def.AddNewly && disc[id] != "") {
				known := make([]string, 0, len(infos))
				for _, x := range infos {
					known = append(known, x.AccountID)
				}
				sort.Strings(known)
				shown, _ := result.CapRow(known, 12)
				return nil, nil, fmt.Errorf("%w: %s is not a member account of %q; its accounts include: %s", ErrInvalidInput, id, in.Name, strings.Join(shown, ", "))
			}
			_ = i
		}
		var added []string
		if len(def.Add) > 0 || def.AddNewly {
			role, err := roleNameOf(cur.AssumeRoleInfos)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
			}
			want := def.Add
			if def.AddNewly {
				want = want[:0:0]
				for id := range disc {
					want = append(want, id)
				}
				sort.Strings(want)
			}
			for _, id := range want {
				if _, member := index[id]; member {
					return nil, nil, fmt.Errorf("%w: %s is already a member account of %q", ErrInvalidInput, id, in.Name)
				}
				name, newly := disc[id]
				if !newly {
					return nil, nil, fmt.Errorf("%w: %s is not newly discovered for %q (the accounts the latest snapshot saw that the setup does not list)", ErrInvalidInput, id, in.Name)
				}
				infos = append(infos, forward.AWSAssumeRoleInfo{AccountID: id, AccountName: name, RoleARN: "arn:aws:iam::" + id + ":role/" + role, Enabled: true})
				index[id] = len(infos) - 1
				added = append(added, id)
			}
		}
		for _, id := range def.ExternalIDs {
			i, ok := index[id]
			if !ok {
				return nil, nil, fmt.Errorf("%w: %s is not a member account of %q", ErrInvalidInput, id, in.Name)
			}
			if vals != nil {
				if v := vals[id]; v == nil {
					infos[i].ExternalID = ""
				} else {
					infos[i].ExternalID = *v
				}
			}
			changedIDs = append(changedIDs, id)
		}
		for id, on := range def.AccountsEnabled {
			i, ok := index[id]
			if !ok {
				return nil, nil, fmt.Errorf("%w: %s is not a member account of %q", ErrInvalidInput, id, in.Name)
			}
			infos[i].Enabled = on
		}
		summary["added"] = added
		summary["members_before"], summary["members_after"] = len(cur.AssumeRoleInfos), len(infos)
		if len(changedIDs) > 0 {
			ext := map[string]string{}
			for _, id := range changedIDs {
				switch {
				case vals == nil:
					ext[id] = "values not read (dry run without secret_file)"
				case vals[id] == nil:
					ext[id] = "cleared"
				default:
					ext[id] = "set"
				}
			}
			summary["external_ids"] = ext
		}
		return infos, summary, nil
	}

	disc, err := discovered(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the newly discovered accounts failed: %w", err)
	}
	infos, summary, err := compose(*a, disc)
	if err != nil {
		return nil, err
	}
	limits := []string{"the member account list is replaced as a whole on Forward: every other account is resent exactly as it was (role, enabled flag, external id, last error), and the account is read again right before the write so a concurrent change is not lost",
		"external id values are never shown, logged or returned; the result only says set or cleared per account",
		"this changes what the next collection will use; it does not start one (edit-source cloud_account test checks the connection, edit-collection collects)"}
	if len(def.ExternalIDs) > 0 && !valuesRead {
		limits = append(limits, "the values file was not given in this dry run, so whether each account is set or cleared is not shown; apply needs it")
	}
	before := map[string]any{"members": len(a.AssumeRoleInfos)}
	return &networkPlan{target: fmt.Sprintf("edit the member accounts of cloud account %q on network %s", in.Name, in.NetworkID), action: "update_cloud_members", before: before, after: summary, reversible: true,
		undo:   "external ids: set them back with accounts_external_ids (the previous values are not kept here: keep the old values file); added accounts: accounts_enabled false, or remove them in the UI",
		limits: limits,
		do: func(ctx context.Context) error {
			l, _, err := s.Client.CloudAccounts.List(ctx, in.NetworkID)
			if err != nil {
				return err
			}
			var cur *forward.CloudAccount
			for i := range l {
				if l[i].Name == in.Name {
					cur = &l[i]
				}
			}
			if cur == nil {
				return fmt.Errorf("cloud account %q is gone", in.Name)
			}
			disc, err := discovered(ctx)
			if err != nil {
				return err
			}
			fresh, _, err := compose(*cur, disc)
			if err != nil {
				return err
			}
			_, _, err = s.Client.CloudAccounts.Patch(ctx, in.NetworkID, in.Name, forward.CloudAccountPatch{Type: cur.Type, AssumeRoleInfos: &fresh})
			return err
		},
		verify: func(ctx context.Context) (bool, any, error) {
			l, _, e := s.Client.CloudAccounts.List(ctx, in.NetworkID)
			for _, x := range l {
				if x.Name != in.Name {
					continue
				}
				got := map[string]forward.AWSAssumeRoleInfo{}
				for _, m := range x.AssumeRoleInfos {
					got[m.AccountID] = m
				}
				ok := true
				for _, w := range infos {
					g, seen := got[w.AccountID]
					if !seen {
						ok = false
						continue
					}
					if inList(def.ExternalIDs, w.AccountID) && vals != nil && g.ExternalID != w.ExternalID {
						ok = false
					}
					if inList(summaryAdded(summary), w.AccountID) && (g.RoleARN != w.RoleARN || !g.Enabled) {
						ok = false
					}
				}
				return ok, map[string]any{"members": len(x.AssumeRoleInfos)}, e
			}
			return false, nil, e
		}}, nil
}

func inList(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

func summaryAdded(s map[string]any) []string {
	a, _ := s["added"].([]string)
	return a
}

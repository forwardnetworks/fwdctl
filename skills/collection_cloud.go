package skills

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const maxCloudLogBytes = 8 << 20

// cloudClass is one kind of cloud collection failure that can be recognised in the snapshot's error log, with what it means and what to read next.
type cloudClass struct {
	name, means, next string
}

var (
	logLine      = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}[ T][0-9:.,]+\s*Z?)\s+\[[^\]]*\]\s+(\w+)\s+([\w.$]+):\s*(.*)$`)
	capRequest   = regexp.MustCompile("C2S Access Portal credentials request for `?([^`:]+?)`?:")
	httpStatus   = regexp.MustCompile(`HTTP (\d{3})`)
	tlsProblem   = regexp.MustCompile(`(?i)PKIX path|CertPathValidatorException|CertificateExpired|certificate has expired|certificate expired|SSLHandshakeException|unable to find valid certification path|NotAfter|validity check failed|CERTIFICATE_CHECK_FAILED`)
	awsDenied    = regexp.MustCompile(`(?i)AccessDenied|not authorized to perform: sts:AssumeRole|InvalidClientTokenId|ExpiredToken|UnrecognizedClientException`)
	awsThrottle  = regexp.MustCompile(`(?i)Throttling|Rate exceeded|RequestLimitExceeded`)
	azureAuth    = regexp.MustCompile(`AADSTS\d+`)
	gcpAuth      = regexp.MustCompile(`(?i)PERMISSION_DENIED|invalid_grant|iam\.serviceAccounts`)
	cloudContext = regexp.MustCompile(`(?i)aws|amazon|azure|gcp|google cloud|C2S|\.cloud\.`)
	azureError   = regexp.MustCompile(`^\s*([0-9a-fA-F]{8}-[0-9a-fA-F-]{27})\s+Unable to collect ([^:]+):.*?"code"\s*:\s*"(\w+)"`)
)

var cloudClasses = map[string]cloudClass{
	"cap_credentials_http_400": {"cap_credentials_http_400", "the C2S Access Portal answered HTTP 400 to a credentials request for this account: the portal no longer offers the account (or its role), or the account is requested although it is not meant to be collected. Collection requests credentials only for the setup's pinned accounts (c2s_accounts); discovery and the setup test request every portal account on purpose", "inspect-collection view config (the pinned list), then edit-source cloud_account update with c2s_accounts to pin only accounts the portal offers"},
	"cap_credentials_http":     {"cap_credentials_http", "the C2S Access Portal answered an HTTP error to a credentials request for this account", "inspect-collection view config; investigate-collection-failure reference/cloud.md"},
	"cap_portal_certificate":   {"cap_portal_certificate", "a TLS or certificate problem talking to the C2S Access Portal: the portal's certificate or the CA or client certificate configured for it is expired, not yet valid or not trusted. Every account fails at once, which is how a whole AWS collection drops", "inspect-collection view config (cap_ca_set, cap_server_url); renew the CA or client certificate in the Forward UI (the library cannot edit a CAP credential), then run the setup test"},
	"aws_assume_role_denied":   {"aws_assume_role_denied", "AWS refused the credentials or the assume-role for an account: the role's trust policy, the external id, or an expired or invalid key", "inspect-collection view config (member account errors); edit-source cloud_account rotate or update"},
	"aws_throttled":            {"aws_throttled", "AWS throttled the collection's API calls", "retry later; reduce the accounts or regions collected together"},
	"azure_auth":               {"azure_auth", "Azure Active Directory refused the credentials (an AADSTS error)", "edit-source cloud_account rotate; check the app registration, secret expiry and subscription access"},
	"gcp_auth":                 {"gcp_auth", "Google Cloud refused the credentials or the project access", "edit-source cloud_account rotate; check the service account's roles"},
}

// classifyCloudLine says which cloud failure class an ERROR log line belongs to, the account it names when it does, and whether it is a cloud line at all. A line that quotes a device or an HTTP endpoint is not.
func classifyCloudLine(logger, msg string) (class, account string) {
	if m := capRequest.FindStringSubmatch(msg); m != nil {
		account = strings.TrimSpace(m[1])
		if tlsProblem.MatchString(msg) {
			return "cap_portal_certificate", account
		}
		if h := httpStatus.FindStringSubmatch(msg); h != nil {
			if h[1] == "400" {
				return "cap_credentials_http_400", account
			}
			return "cap_credentials_http", account
		}
		return "cap_credentials_http", account
	}
	if strings.Contains(msg, "CERTIFICATE_CHECK_FAILED") || (tlsProblem.MatchString(msg) && regexp.MustCompile(`(?i)C2S|access portal|\bCAP\b`).MatchString(msg)) {
		return "cap_portal_certificate", ""
	}
	if !cloudContext.MatchString(logger+" "+msg) && !regexp.MustCompile(`(?i)AWSSecurityTokenService|sts:AssumeRole|AADSTS`).MatchString(msg) {
		return "", ""
	}
	if m := azureError.FindStringSubmatch(msg); m != nil {
		return "azure_" + m[3], m[1]
	}
	switch {
	case azureAuth.MatchString(msg):
		return "azure_auth", ""
	case gcpAuth.MatchString(msg):
		return "gcp_auth", ""
	case awsThrottle.MatchString(msg):
		return "aws_throttled", ""
	case awsDenied.MatchString(msg):
		return "aws_assume_role_denied", regexp.MustCompile(`\b\d{12}\b`).FindString(msg)
	}
	return "", ""
}

// cloudCollectionErrors is view cloud of investigate-collection-failure: the snapshot's error log scanned for the failures of cloud collection, rolled up by class and account, with the cloud setups' own state beside it.
func cloudCollectionErrors(ctx context.Context, s *fwd.Session, in collectionInput, cx result.Context) (result.Result, error) {
	var buf bytes.Buffer
	_, truncated, _, err := s.Client.Snapshots.Logs(ctx, fwd.SnapshotID(cx), forwardLogOptions("ERROR"), maxCloudLogBytes, &buf)
	if fwd.NotFound(err) || fwd.NotAcceptable(err) {
		return result.NewUnknown(collectionFailureName, "Forward holds no collection log for this snapshot", cx,
			[]string{"Forward answered 404 or 406 for the log: an imported, forked or reprocessed snapshot keeps no collection log"}, result.Options{NextActions: []string{"inspect-collection"}})
	}
	if err != nil {
		return result.Result{}, err
	}
	type agg struct {
		lines           int
		accounts        map[string]bool
		first, last, ex string
	}
	by := map[string]*agg{}
	total := 0
	for _, raw := range strings.Split(buf.String(), "\n") {
		m := logLine.FindStringSubmatch(raw)
		if m == nil || !strings.EqualFold(m[2], "ERROR") {
			continue
		}
		class, account := classifyCloudLine(m[3], m[4])
		if class == "" {
			continue
		}
		total++
		a := by[class]
		if a == nil {
			a = &agg{accounts: map[string]bool{}, first: strings.TrimSpace(m[1]), ex: oneLineNote(m[4])}
			by[class] = a
		}
		a.lines++
		a.last = strings.TrimSpace(m[1])
		if account != "" {
			a.accounts[account] = true
		}
	}
	var limits []string
	if truncated {
		limits = append(limits, fmt.Sprintf("the error log was read to %d MiB and cut; later lines were not scanned, so the counts are low", maxCloudLogBytes>>20))
	}
	// the setups beside the log: which are CAP, how many accounts each pins
	setupNotes, setups := cloudSetupContext(ctx, s, in.NetworkID)
	limits = append(limits, setupNotes...)
	if total == 0 {
		return result.NewUnknown(collectionFailureName, "No cloud collection error was found in this snapshot's error log", cx,
			append(limits, "the error log holds no line recognised as a cloud failure (C2S Access Portal, AWS, Azure or GCP): that is not proof the cloud collection worked, since a collection that never started logs nothing. Read inspect-inventory kind cloud_coverage for what was collected"),
			result.Options{NextActions: []string{"inspect-inventory", "inspect-collection"}})
	}
	names := make([]string, 0, len(by))
	for k := range by {
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool { return by[names[i]].lines > by[names[j]].lines })
	classes := make([]map[string]any, 0, len(names))
	var next []string
	for _, k := range names {
		a := by[k]
		accts := make([]string, 0, len(a.accounts))
		for ac := range a.accounts {
			accts = append(accts, ac)
		}
		sort.Strings(accts)
		shown, capped := result.Cap(accts, 25, "accounts named in the log", "the first 25 are listed")
		_ = capped
		cls, known := cloudClasses[k]
		if !known && strings.HasPrefix(k, "azure_") {
			cls = cloudClass{k, "Azure Resource Manager refused or could not serve a request for this subscription (" + strings.TrimPrefix(k, "azure_") + "). The accounts listed are subscription ids", "inspect-inventory kind cloud_coverage names each uncollected subscription and what the setup records about it; edit-source cloud_account update to change the subscription list"}
			if k == "azure_SubscriptionNotFound" {
				cls.means = "Azure says the subscription does not exist for these credentials: it was deleted, moved to another tenant, or the credentials' identity has no role on it. The accounts listed are subscription ids"
			}
		}
		classes = append(classes, map[string]any{"class": k, "lines": a.lines, "accounts": nonNilStrings(shown), "accounts_total": len(accts), "first": a.first, "last": a.last, "example": a.ex, "means": cls.means, "next": cls.next})
		if k != "aws_throttled" && k != "cap_portal_certificate" {
			next = append(next, "edit-source")
		}
	}
	if by["cap_credentials_http_400"] != nil {
		limits = append(limits, "the accounts named are the account NAMES in the log; the setup pins accounts by id and role (c2s_accounts), which the log does not map. To match them, run the setup's account discovery from the Forward UI. The same error line is also written by discovery and by the setup test, so check the times against a collection before blaming it")
	}
	if by["cap_portal_certificate"] != nil {
		limits = append(limits, "the library cannot read when the portal CA or client certificate expires (Forward returns them only to the UI); the log lines above are the evidence")
	}
	finding := fmt.Sprintf("%d cloud collection error line(s) in %d class(es); the most frequent is %s", total, len(classes), names[0])
	detail := map[string]any{"classes": classes, "error_lines": total, "cloud_setups": setups}
	return result.Build(collectionFailureName, result.Failed, finding, result.Deterministic, cx, result.Options{Limits: limits, NextActions: dedupe(append([]string{"inspect-collection", "inspect-inventory"}, next...)),
		Evidence: []result.Evidence{result.NewEvidence(result.EvCollection, "snapshotLogs", cx.SnapshotID, detail, finding)}})
}

func nonNilStrings(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

func forwardLogOptions(level string) forward.SnapshotLogOptions {
	return forward.SnapshotLogOptions{Level: level}
}

// cloudSetupContext lists the network's cloud setups beside the log: type, whether it collects, and for a C2S Access Portal setup how many accounts it pins. A failed read is a limit, not a stop.
func cloudSetupContext(ctx context.Context, s *fwd.Session, networkID string) ([]string, []map[string]any) {
	list, _, err := s.Client.CloudAccounts.List(ctx, networkID)
	if err != nil {
		return []string{"the cloud setups could not be read, so the log is shown without their configuration: " + err.Error()}, nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, a := range list {
		row := map[string]any{"name": a.Name, "type": a.Type, "collect": a.Collect}
		if a.HasCAPCredential() {
			row["cap_credential"] = true
			row["c2s_accounts_pinned"] = len(a.AWSC2SAccountInfos)
		}
		out = append(out, row)
	}
	return nil, out
}

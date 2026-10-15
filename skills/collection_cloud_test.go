package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// view cloud reads the snapshot's error log for the failures of cloud collection and rolls them up by class and account: a C2S Access Portal credentials request that fails for an account (HTTP 400 is the
// signature of an account the portal no longer offers or the setup should not request), a certificate problem with the portal (expired, untrusted), a failed assume-role, an Azure or GCP authentication
// error. The rest of the error log (device and endpoint errors) is not its business.
const cloudLog = "" +
	"2026-10-13 09:01:00 Z [collector-worker-1] ERROR com.forwardnetworks.client.device.f.f: Exception during C2S Access Portal credentials request for `acct-one`: (UNKNOWN) HTTP 400\n" +
	"retrofit2.HttpException: HTTP 400\n" +
	"\tat com.forwardnetworks.client.device.f.AwsC2SAuthenticator.a(AwsC2SAuthenticator.java:68)\n" +
	"2026-10-13 09:01:05 Z [collector-worker-2] ERROR com.forwardnetworks.client.device.f.f: Exception during C2S Access Portal credentials request for `acct-two`: (UNKNOWN) HTTP 400\n" +
	"2026-10-13 09:01:09 Z [collector-worker-2] ERROR com.forwardnetworks.client.device.f.f: Exception during C2S Access Portal credentials request for `acct-one`: (UNKNOWN) HTTP 400\n" +
	"2026-10-13 09:02:00 Z [collector-worker-3] ERROR com.forwardnetworks.client.device.f.f: Exception during C2S Access Portal credentials request for `acct-three`: javax.net.ssl.SSLHandshakeException: PKIX path validation failed: java.security.cert.CertPathValidatorException: validity check failed\n" +
	"2026-10-13 09:03:00 Z [collector-worker-4] ERROR com.forwardnetworks.client.cloud.aws.A: AssumeRole failed for account 111122223333: User is not authorized to perform: sts:AssumeRole (Service: AWSSecurityTokenService; Status Code: 403; Error Code: AccessDenied)\n" +
	"2026-10-13 09:03:20 Z [collector-worker-7] ERROR com.forwardnetworks.client.cloud.aws.A: collection error CERTIFICATE_CHECK_FAILED for the access portal\n" +
	"2026-10-13 09:03:30 Z [collector-worker-6] ERROR com.forwardnetworks.client.device.g.AzureApi: 11111111-2222-3333-4444-555555555555 Unable to collect ResourceManager.resourceGroups: Status code 404, \"{\"error\":{\"code\":\"SubscriptionNotFound\",\"message\":\"The subscription was not found.\"}}\"\n" +
	"2026-10-13 09:04:00 Z [collector-worker-5] ERROR com.forwardnetworks.client.source.http.M: Failed to read page 1 for HttpEndpoint(name=x, path=/aaa-confg)\n"

func cloudRoutes() map[string]fwdtest.Handler {
	routes := cfRoutes("PROCESSED", nil, nil)
	routes["GET /api/snapshots/s1/logs"] = func(r *http.Request, _ []byte) (int, any) {
		if r.URL.Query().Get("level") != "ERROR" || r.URL.Query().Get("deviceName") != "" {
			return 200, []byte("")
		}
		return 200, []byte(cloudLog)
	}
	return routes
}

func TestCollectionCloudViewRollsUpCloudErrorsByClassAndAccount(t *testing.T) {
	r, _ := collect(t, cloudRoutes(), `{"network_id":"n1","view":"cloud"}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "cloud") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	classes, _ := r.Evidence[0].Detail["classes"].([]map[string]any)
	by := map[string]map[string]any{}
	for _, c := range classes {
		by[c["class"].(string)] = c
	}
	cap400 := by["cap_credentials_http_400"]
	if cap400 == nil || cap400["lines"] != 3 || len(cap400["accounts"].([]string)) != 2 {
		t.Errorf("the CAP HTTP 400 class counts 3 lines for 2 accounts: %v", cap400)
	}
	if az := by["azure_SubscriptionNotFound"]; az == nil || az["accounts"].([]string)[0] != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("an Azure SubscriptionNotFound names the subscription id: %v", by["azure_SubscriptionNotFound"])
	}
	if by["cap_portal_certificate"] == nil || by["aws_assume_role_denied"] == nil {
		t.Errorf("classes = %v", by)
	}
	if _, device := by["device_or_endpoint"]; device {
		t.Errorf("a device error is not a cloud error: %v", by)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "not the same as") && !strings.Contains(strings.Join(r.NextActions, " "), "edit-source") {
		t.Errorf("the CAP finding should point at the fix: limits=%v next=%v", r.Limits, r.NextActions)
	}
}

func TestCollectionCloudViewWithNoCloudErrorsIsUnknownNotHealthy(t *testing.T) {
	routes := cloudRoutes()
	routes["GET /api/snapshots/s1/logs"] = func(*http.Request, []byte) (int, any) {
		return 200, []byte("2026-10-13 09:04:00 Z [w] ERROR com.forwardnetworks.client.source.http.M: Failed to read page 1\n")
	}
	r, _ := collect(t, routes, `{"network_id":"n1","view":"cloud"}`)
	if r.Status != result.Unknown {
		t.Fatalf("no cloud error line in the error log is not proof the cloud collection worked: %s %s", r.Status, r.Finding)
	}
}

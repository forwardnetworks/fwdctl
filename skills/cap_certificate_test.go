package skills_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/forwardnetworks/fwdctl/fwdtest"
)

// A CAP setup's CA expiry is shown (earliest NotAfter, days left) and a CA that has expired or expires within 30 days is a limit that names the consequence: every account fails at once. The client certificate's
// expiry cannot be read, and the result says why instead of implying it is fine.
func caBundle(t *testing.T, notAfter time.Time) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"}, NotBefore: notAfter.AddDate(-2, 0, 0), NotAfter: notAfter, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func capRoutes(ca string) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/classic-devices": fwdtest.Const(200, []any{}),
		"GET /api/networks/n1/cloudAccounts": fwdtest.Const(200, []any{map[string]any{"type": "AWS", "name": "aws-cap", "collect": true, "awsCapCredentialId": "cap-1",
			"awsC2SAccountInfos": []any{map[string]any{"accountId": "111111111111", "accountRole": "r"}}}}),
		"GET /api/networks/n1/aws-cap-credentials/cap-1": fwdtest.Const(200, map[string]any{"id": "cap-1", "baseCapServerUrl": "https://cap.example", "certificateAuthorityBase64": ca, "clientCertificateBase64": "AAAA"}),
	}
}

func TestCollectionConfigShowsTheCAPCertificateExpiryAndWarnsWhenItIsNear(t *testing.T) {
	for name, c := range map[string]struct {
		notAfter time.Time
		want     string
	}{
		"expired": {time.Now().AddDate(0, 0, -3), "EXPIRED"},
		"near":    {time.Now().AddDate(0, 0, 12), "expires in"},
		"far":     {time.Now().AddDate(1, 0, 0), ""},
	} {
		r, _ := mustRun(t, "inspect-collection-config", capRoutes(caBundle(t, c.notAfter)), `{"network_id":"n1"}`)
		all := jsonOf(r)
		if !strings.Contains(all, `"cap_ca_not_after"`) || !strings.Contains(all, `"cap_ca_days_left"`) {
			t.Errorf("%s: the CA expiry should be shown: %s", name, all)
		}
		warned := strings.Contains(strings.Join(r.Limits, " "), "C2S Access Portal CA")
		if (c.want != "") != warned || (c.want != "" && !strings.Contains(strings.Join(r.Limits, " "), c.want)) {
			t.Errorf("%s: warned=%v limits=%v", name, warned, r.Limits)
		}
		if !strings.Contains(strings.Join(r.Limits, " "), "client certificate") {
			t.Errorf("%s: the client certificate's unknown expiry should be said: %v", name, r.Limits)
		}
		if strings.Contains(all, "AAAA") {
			t.Errorf("%s: certificate bytes must never appear", name)
		}
	}
}

package infra

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// adcWithQuota points Application Default Credentials at a credential file
// that names quota as its quota project, whose token endpoint is a fake (a
// service-account key made here: the library honours its token_uri).
func adcWithQuota(t *testing.T, quota string) {
	t.Helper()
	tok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer", "expires_in": 3600})
	}))
	t.Cleanup(tok.Close)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pk := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: mustPKCS8(t, key)})
	cred, _ := json.Marshal(map[string]string{"type": "service_account", "project_id": "adc", "private_key_id": "k",
		"private_key": string(pk), "client_email": "sa@adc.iam.gserviceaccount.com", "token_uri": tok.URL, "quota_project_id": quota})
	path := filepath.Join(t.TempDir(), "adc.json")
	if err := os.WriteFile(path, cred, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
}

// Live Check 27: a first run read the new project's billing with the new
// project as the quota project, and the Cloud Billing API is not enabled on a
// project that does not exist yet. The billing reads are billed to the
// quota project of the credentials, whatever the target project is.
func TestBillingClientUsesTheCredentialsQuotaProject(t *testing.T) {
	adcWithQuota(t, "adc-quota")
	var got []string
	bill := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("X-Goog-User-Project"))
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "projects/new-proj/billingInfo", "billingEnabled": true})
	}))
	t.Cleanup(bill.Close)
	c, err := NewClients(context.Background(), gcp.Options{GCPProject: "new-proj", Region: "us-east5"}, Endpoints{Billing: bill.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Billing.Projects.GetBillingInfo("projects/new-proj").Context(context.Background()).Do(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "adc-quota" {
		t.Fatalf("billing read sent X-Goog-User-Project %q, want the credentials' adc-quota", got)
	}
}

func mustPKCS8(t *testing.T, k *rsa.PrivateKey) []byte {
	t.Helper()
	b, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

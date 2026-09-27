package tfwrap

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/siderolabs/terraform-provider-talos/pkg/talos"
)

const kubeconfigTypeName = "talos_cluster_kubeconfig"

// selfSignedB64 returns a base64-encoded PEM certificate, the shape Talos
// stores in kubernetes_client_configuration.client_certificate.
func selfSignedB64(t *testing.T, notAfter time.Time) string {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "admin"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// readKubeconfig runs a proto6 ReadResource for talos_cluster_kubeconfig
// against a configured provider server and reports whether the resource is
// still present in the returned state.
func readKubeconfig(t *testing.T, p provider.Provider, clientCert string, renewal any) bool {
	t.Helper()
	ctx := context.Background()
	srv := providerserver.NewProtocol6(p)()
	ps, err := srv.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	pty := ps.Provider.ValueType()
	pv, err := tfprotov6.NewDynamicValue(pty, tftypes.NewValue(pty, map[string]tftypes.Value{"image_factory_url": tftypes.NewValue(tftypes.String, nil)}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{Config: &pv}); err != nil {
		t.Fatal(err)
	}
	sr := &fwresource.SchemaResponse{}
	talos.NewTalosClusterKubeConfigResource().Schema(ctx, fwresource.SchemaRequest{}, sr)
	ty := sr.Schema.Type().TerraformType(ctx)

	// The state as upjet rebuilds it from status.atProvider after a restart.
	prior := map[string]any{
		"id":       "c1",
		"node":     "10.0.0.2",
		"endpoint": "10.0.0.2",
		"client_configuration": map[string]any{
			"ca_certificate": "Y2E=", "client_certificate": "Y2VydA==", "client_key": "a2V5",
		},
		"kubernetes_client_configuration": map[string]any{
			"host":               "https://10.0.0.2:6443",
			"ca_certificate":     selfSignedB64(t, time.Now().AddDate(10, 0, 0)),
			"client_certificate": clientCert,
		},
		"certificate_renewal_duration": renewal,
	}
	b, err := json.Marshal(prior)
	if err != nil {
		t.Fatal(err)
	}
	v, err := tftypes.ValueFromJSONWithOpts(b, ty, tftypes.ValueFromJSONOpts{IgnoreUndefinedAttributes: true})
	if err != nil {
		t.Fatal(err)
	}
	dv, err := tfprotov6.NewDynamicValue(ty, v)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: kubeconfigTypeName, CurrentState: &dv})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range resp.Diagnostics {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			t.Fatalf("read diag: %s: %s", d.Summary, d.Detail)
		}
	}
	nv, err := resp.NewState.Unmarshal(ty)
	if err != nil {
		t.Fatal(err)
	}
	return !nv.IsNull()
}

func TestKubeconfigRenewalWindow(t *testing.T) {
	in10d := time.Now().Add(10 * 24 * time.Hour)
	in1y := time.Now().AddDate(1, 0, 0)

	for _, tc := range []struct {
		name        string
		p           provider.Provider
		cert        string
		renewal     any
		wantPresent bool
	}{
		{"upstream: cert inside the window stays present (renewal dropped)", talos.New(), selfSignedB64(t, in10d), "720h", true},
		{"wrapped: cert inside the default 720h window is removed", New(), selfSignedB64(t, in10d), "720h", false},
		{"wrapped: unset renewal duration falls back to 720h", New(), selfSignedB64(t, in10d), nil, false},
		{"wrapped: cert outside the window stays present", New(), selfSignedB64(t, in1y), "720h", true},
		{"wrapped: a shorter renewal duration is honoured", New(), selfSignedB64(t, in10d), "24h", true},
		{"wrapped: a longer renewal duration is honoured", New(), selfSignedB64(t, in1y), "8784h", false},
		{"wrapped: an unparsable certificate never removes the resource", New(), "bm90IGEgY2VydA==", "720h", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := readKubeconfig(t, tc.p, tc.cert, tc.renewal); got != tc.wantPresent {
				t.Fatalf("present=%v, want %v", got, tc.wantPresent)
			}
		})
	}
}

func TestKubeconfigRenewalUsesClock(t *testing.T) {
	cert := selfSignedB64(t, time.Now().AddDate(1, 0, 0))
	if !readKubeconfig(t, New(), cert, "720h") {
		t.Fatal("a one-year certificate must be present today")
	}
	orig := now
	t.Cleanup(func() { now = orig })
	now = func() time.Time { return time.Now().Add(340 * 24 * time.Hour) }
	if readKubeconfig(t, New(), cert, "720h") {
		t.Fatal("the same certificate must be inside the window 340 days later")
	}
}

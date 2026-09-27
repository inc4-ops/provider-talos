package offlinetest

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"sort"
	"testing"
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/siderolabs/talos/pkg/machinery/config/configpatcher"
	"github.com/siderolabs/terraform-provider-talos/pkg/talos"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pconfig "github.com/inc4-ops/provider-talos/config"
	"github.com/inc4-ops/provider-talos/internal/tfwrap"
)

// renderedHash is the machine_configuration_hash upstream computes: sha256 of
// the base config with the patches applied.
func renderedHash(t *testing.T, base string, patches []any) string {
	t.Helper()
	ps := make([]string, 0, len(patches))
	for _, p := range patches {
		ps = append(ps, p.(string))
	}
	lp, err := configpatcher.LoadPatches(ps)
	if err != nil {
		t.Fatal(err)
	}
	out, err := configpatcher.Apply(configpatcher.WithBytes([]byte(base)), lp)
	if err != nil {
		t.Fatal(err)
	}
	b, err := out.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func observeApply(t *testing.T, fw fwprovider.Provider, params, observation map[string]any) managed.ExternalObservation {
	t.Helper()
	cfg := pconfig.GetProviderNamespaced().Resources["talos_machine_configuration_apply"]
	o := mrOpts{name: "n1", params: params, observation: observation}
	if observation != nil {
		o.externalID = "machine_configuration_apply"
	}
	mr := newMR(o)
	obs, err := connect(t, newKube(), fw, cfg, mr).Observe(context.Background(), mr)
	if err != nil {
		t.Fatal(err)
	}
	return obs
}

// TestApplyAfterRestart covers the re-apply found in the provider design:
// after a restart upjet rebuilds the state from status.atProvider, which has
// no machine_configuration (sensitive output), so upstream plans a diff and
// every node would be re-applied. The wrapper keeps the plan empty while the
// rendered hash is unchanged, and still reports a changed patch.
func TestApplyAfterRestart(t *testing.T) {
	b := baseConfig(t)
	patches := []any{"machine:\n  nodeLabels:\n    pool: gpu\n"}
	params := map[string]any{"node": "10.0.0.2", "machine_configuration_input": b, "config_patches": patches, "client_configuration": clientConfiguration}

	if obs := observeApply(t, tfwrap.New(), params, nil); obs.ResourceExists {
		t.Fatal("fresh MR reported as existing")
	}

	restored := map[string]any{
		"id": "machine_configuration_apply", "node": "10.0.0.2", "endpoint": "10.0.0.2", "apply_mode": "auto",
		"resolved_apply_mode": "auto", "config_patches": patches, "machine_configuration_hash": renderedHash(t, b, patches),
		// sensitive parameters, which upjet restores from the spec secret refs
		"machine_configuration_input": b, "client_configuration": clientConfiguration,
	}

	obs := observeApply(t, talos.New(), params, restored)
	t.Logf("upstream after restart: exists=%v upToDate=%v", obs.ResourceExists, obs.ResourceUpToDate)
	if !obs.ResourceExists || obs.ResourceUpToDate {
		t.Fatalf("expected upstream to report a diff after restart (exists=%v upToDate=%v)", obs.ResourceExists, obs.ResourceUpToDate)
	}

	obs = observeApply(t, tfwrap.New(), params, restored)
	t.Logf("wrapped after restart: exists=%v upToDate=%v", obs.ResourceExists, obs.ResourceUpToDate)
	if !obs.ResourceExists || !obs.ResourceUpToDate {
		t.Fatalf("expected the wrapped provider to be up to date after restart (exists=%v upToDate=%v)", obs.ResourceExists, obs.ResourceUpToDate)
	}

	changed := map[string]any{}
	for k, v := range params {
		changed[k] = v
	}
	changed["config_patches"] = []any{"machine:\n  nodeLabels:\n    pool: cpu\n"}
	obs = observeApply(t, tfwrap.New(), changed, restored)
	t.Logf("wrapped, changed patch: exists=%v upToDate=%v", obs.ResourceExists, obs.ResourceUpToDate)
	if obs.ResourceUpToDate {
		t.Fatal("a changed patch must produce a diff")
	}
}

func selfSigned(t *testing.T, notAfter time.Time) string {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "admin"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// kubeconfigObservation is status.atProvider of a Ready Kubeconfig MR: no
// sensitive attribute (kubeconfig_raw, client_key) survives there.
func kubeconfigObservation(t *testing.T, clientCertNotAfter time.Time) map[string]any {
	t.Helper()
	return map[string]any{
		"id": "c1", "node": "10.0.0.2", "endpoint": "10.0.0.2", "certificate_renewal_duration": "720h", "client_configuration": clientConfiguration,
		"kubernetes_client_configuration": map[string]any{
			"host":               "https://10.0.0.2:6443",
			"ca_certificate":     selfSigned(t, time.Now().AddDate(10, 0, 0)),
			"client_certificate": selfSigned(t, clientCertNotAfter),
		},
	}
}

func observeKubeconfig(t *testing.T, fw fwprovider.Provider, clientCertNotAfter time.Time) managed.ExternalObservation {
	t.Helper()
	cfg := pconfig.GetProviderNamespaced().Resources["talos_cluster_kubeconfig"]
	mr := newMR(mrOpts{
		name:        "kc",
		params:      map[string]any{"node": "10.0.0.2", "client_configuration": clientConfiguration},
		observation: kubeconfigObservation(t, clientCertNotAfter),
		externalID:  "c1",
	})
	obs, err := connect(t, newKube(), fw, cfg, mr).Observe(context.Background(), mr)
	if err != nil {
		t.Fatal(err)
	}
	return obs
}

// TestKubeconfigRenewal: upstream silently drops the renewal under upjet; the
// wrapper reports the resource absent inside the window so upjet re-creates it.
func TestKubeconfigRenewal(t *testing.T) {
	soon := time.Now().Add(10 * 24 * time.Hour)
	later := time.Now().AddDate(1, 0, 0)

	obs := observeKubeconfig(t, talos.New(), soon)
	t.Logf("upstream, cert expires in 10d: exists=%v upToDate=%v (renewal silently dropped)", obs.ResourceExists, obs.ResourceUpToDate)
	if !obs.ResourceExists || !obs.ResourceUpToDate {
		t.Fatalf("upstream behaviour changed (exists=%v upToDate=%v): re-check whether the wrapper is still needed", obs.ResourceExists, obs.ResourceUpToDate)
	}

	obs = observeKubeconfig(t, tfwrap.New(), soon)
	t.Logf("wrapped, cert expires in 10d: exists=%v upToDate=%v", obs.ResourceExists, obs.ResourceUpToDate)
	if obs.ResourceExists {
		t.Fatal("a kubeconfig inside the renewal window must report absent so upjet re-creates it")
	}

	obs = observeKubeconfig(t, tfwrap.New(), later)
	t.Logf("wrapped, cert expires in 1y: exists=%v upToDate=%v", obs.ResourceExists, obs.ResourceUpToDate)
	if !obs.ResourceExists || !obs.ResourceUpToDate {
		t.Fatal("a fresh kubeconfig must be existing and up to date")
	}
}

// TestKubeconfigSecretSurvivesRestart ports phase B of the verdict spike to
// the generated Kubeconfig kind: after a provider restart the rebuilt state
// has no kubeconfig_raw, and publishing the Observe's connection details must
// leave every key of the connection Secret byte-identical.
func TestKubeconfigSecretSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	kube := newKube()
	cfg := pconfig.GetProviderNamespaced().Resources["talos_cluster_kubeconfig"]
	mr := newMR(mrOpts{
		name:        "kc",
		params:      map[string]any{"node": "10.0.0.2", "client_configuration": clientConfiguration},
		observation: kubeconfigObservation(t, time.Now().AddDate(1, 0, 0)),
		externalID:  "c1",
		mapping:     cfg.Sensitive.GetFieldPaths(),
		connSecret:  "kc-conn",
	})

	// The Secret as the first provider process published it after Create.
	kubeconfig := []byte("apiVersion: v1\nkind: Config\nclusters: []\n")
	written := map[string][]byte{
		"attribute.kubeconfig_raw": kubeconfig,
		"kubeconfig":               kubeconfig,
		"host":                     []byte("https://10.0.0.2:6443"),
		"attribute.kubernetes_client_configuration.client_key": []byte("a3ViZWtleQ=="),
		"attribute.client_configuration.ca_certificate":        []byte("Y2E="),
		"attribute.client_configuration.client_certificate":    []byte("Y2VydA=="),
		"attribute.client_configuration.client_key":            []byte("a2V5"),
	}
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kc-conn", Namespace: "ns",
			OwnerReferences: []metav1.OwnerReference{{APIVersion: gvk.GroupVersion().String(), Kind: gvk.Kind, Name: mr.Name, UID: mr.UID, Controller: ptr.To(true)}},
		},
		Type: "connection.crossplane.io/v1alpha1",
		Data: written,
	}
	if err := kube.Create(ctx, existing); err != nil {
		t.Fatal(err)
	}

	obs, err := connect(t, kube, tfwrap.New(), cfg, mr).Observe(ctx, mr)
	if err != nil {
		t.Fatal(err)
	}
	if !obs.ResourceExists || !obs.ResourceUpToDate {
		t.Fatalf("expected exists and up to date after restart (exists=%v upToDate=%v)", obs.ResourceExists, obs.ResourceUpToDate)
	}
	keys := make([]string, 0, len(obs.ConnectionDetails))
	for k := range obs.ConnectionDetails {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t.Logf("connection details after restart: %v", keys)
	if _, ok := obs.ConnectionDetails["kubeconfig"]; ok {
		t.Fatal("kubeconfig must not be re-emitted from a rebuilt state (it would be empty or stale)")
	}

	if _, err := managed.NewAPILocalSecretPublisher(kube, kube.Scheme()).PublishConnection(ctx, mr, obs.ConnectionDetails); err != nil {
		t.Fatal(err)
	}
	after := &corev1.Secret{}
	if err := kube.Get(ctx, client.ObjectKey{Namespace: "ns", Name: "kc-conn"}, after); err != nil {
		t.Fatal(err)
	}
	if len(after.Data) != len(written) {
		t.Errorf("Secret has %d keys after the restart publish, want %d", len(after.Data), len(written))
	}
	for k, v := range written {
		if !bytes.Equal(after.Data[k], v) {
			t.Errorf("key %s changed after the restart publish: %q -> %q", k, v, after.Data[k])
		}
	}
}

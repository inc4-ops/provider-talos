// Package offlinetest drives the generated resources through the real upjet
// v2.5.0 plugin-framework connector and the real (wrapped) talos provider
// against a fake Kubernetes client. Nothing dials a Talos node.
package offlinetest

import (
	"context"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpresource "github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	ujconfig "github.com/crossplane/upjet/v2/pkg/config"
	ujcontroller "github.com/crossplane/upjet/v2/pkg/controller"
	"github.com/crossplane/upjet/v2/pkg/resource/fake"
	"github.com/crossplane/upjet/v2/pkg/terraform"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	cfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	pconfig "github.com/inc4-ops/provider-talos/config"
	"github.com/inc4-ops/provider-talos/internal/tfwrap"
)

var gvk = schema.GroupVersionKind{Group: "machine.talos.m.inc4.net", Version: "v1alpha1", Kind: "ModernTerraformed"}

// clientConfiguration is a dummy talosconfig; nothing here dials a node.
var clientConfiguration = map[string]any{"ca_certificate": "Y2E=", "client_certificate": "Y2VydA==", "client_key": "a2V5"}

// baseConfig renders a Talos 1.14 worker config with a fresh secrets bundle.
func baseConfig(t *testing.T) string {
	t.Helper()
	sb, err := secrets.NewBundle(secrets.NewClock(), config.TalosVersionCurrent)
	if err != nil {
		t.Fatal(err)
	}
	in, err := generate.NewInput("c1", "https://10.0.0.1:6443", "1.36.5", generate.WithSecretsBundle(sb))
	if err != nil {
		t.Fatal(err)
	}
	p, err := in.Config(machine.TypeWorker)
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func newKube() client.Client {
	sch := runtime.NewScheme()
	_ = corev1.AddToScheme(sch)
	sch.AddKnownTypeWithName(gvk, &fake.ModernTerraformed{})
	return cfake.NewClientBuilder().WithScheme(sch).Build()
}

type mrOpts struct {
	name        string
	params      map[string]any
	observation map[string]any
	externalID  string
	mapping     map[string]string
	connSecret  string
}

func newMR(o mrOpts) *fake.ModernTerraformed {
	mr := &fake.ModernTerraformed{}
	mr.TypeMeta = metav1.TypeMeta{APIVersion: gvk.GroupVersion().String(), Kind: gvk.Kind}
	mr.Name, mr.Namespace, mr.UID = o.name, "ns", types.UID("uid-"+o.name)
	mr.Parameters = o.params
	mr.Observation = o.observation
	mr.ConnectionDetailsMapping = o.mapping
	if mr.ConnectionDetailsMapping == nil {
		mr.ConnectionDetailsMapping = map[string]string{}
	}
	if o.externalID != "" {
		mr.SetAnnotations(map[string]string{"crossplane.io/external-name": o.externalID})
	}
	if o.connSecret != "" {
		mr.LocalConnectionSecretWriterTo.Ref = &xpv2.LocalSecretReference{Name: o.connSecret}
	}
	return mr
}

// connect builds a fresh connector (a new operation store, as after a
// provider restart) and connects it to mr.
func connect(t *testing.T, kube client.Client, fw fwprovider.Provider, cfg *ujconfig.Resource, mr *fake.ModernTerraformed) managed.ExternalClient {
	t.Helper()
	setup := func(_ context.Context, _ client.Client, _ xpresource.Managed) (terraform.Setup, error) {
		return terraform.Setup{FrameworkProvider: fw, Configuration: map[string]any{}}, nil
	}
	nop := logging.NewNopLogger()
	c := ujcontroller.NewTerraformPluginFrameworkConnector(kube, setup, cfg, ujcontroller.NewOperationStore(nop), ujcontroller.WithTerraformPluginFrameworkLogger(nop))
	ext, err := c.Connect(context.Background(), mr)
	if err != nil {
		t.Fatal(err)
	}
	return ext
}

// TestFreshMRsAreAbsent checks the first Observe of a brand-new MR of every
// kind: with the id-based IsStateEmptyFn it must report "absent" without an
// error, so upjet calls Create. Without it, upstream Reads that echo the
// prior state make the Observe fail or report a phantom resource.
func TestFreshMRsAreAbsent(t *testing.T) {
	cases := map[string]map[string]any{
		"talos_machine_configuration_apply": {"node": "10.0.0.2", "machine_configuration_input": baseConfig(t), "client_configuration": clientConfiguration},
		"talos_cluster":                     {"node": "10.0.0.2", "kubernetes_version": "v1.36.5", "client_configuration": clientConfiguration},
		"talos_cluster_kubeconfig":          {"node": "10.0.0.2", "client_configuration": clientConfiguration},
		"talos_image_factory_schematic":     {"schematic": "customization: {}\n"},
	}
	if got, want := len(cases), len(pconfig.ExternalNameConfigs); got != want {
		t.Fatalf("test covers %d kinds, provider exposes %d", got, want)
	}
	for name, params := range cases {
		for _, withFn := range []bool{false, true} {
			cfg := pconfig.GetProviderNamespaced().Resources[name]
			if !withFn {
				cfg.TerraformPluginFrameworkIsStateEmptyFn = nil
			}
			mr := newMR(mrOpts{name: "fresh", params: params})
			o, err := connect(t, newKube(), tfwrap.New(), cfg, mr).Observe(context.Background(), mr)
			t.Logf("%-34s isStateEmptyFn=%-5v exists=%v err=%v", name, withFn, o.ResourceExists, err)
			if withFn && (err != nil || o.ResourceExists) {
				t.Fatalf("%s: expected absent without error, got exists=%v err=%v", name, o.ResourceExists, err)
			}
		}
	}
}

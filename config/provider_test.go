package config

import (
	"sort"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/inc4-ops/provider-talos/config/common"
)

// These invariants are what inc4-platform relies on; a regeneration or an
// upstream bump that breaks one must fail CI, not a cluster.
func TestNamespacedProviderInvariants(t *testing.T) {
	pc := GetProviderNamespaced()
	want := map[string][2]string{ // TF name -> {short group, kind}
		"talos_machine_configuration_apply": {"machine", "ConfigurationApply"},
		"talos_cluster":                     {"cluster", "Cluster"},
		"talos_cluster_kubeconfig":          {"cluster", "Kubeconfig"},
		"talos_image_factory_schematic":     {"image", "FactorySchematic"},
	}
	got := make([]string, 0, len(pc.Resources))
	for n := range pc.Resources {
		got = append(got, n)
	}
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("resources = %v, want exactly %d", got, len(want))
	}
	for name, gk := range want {
		r, ok := pc.Resources[name]
		if !ok {
			t.Fatalf("%s missing", name)
		}
		if r.ShortGroup != gk[0] || r.Kind != gk[1] {
			t.Errorf("%s: group/kind %s/%s, want %s/%s", name, r.ShortGroup, r.Kind, gk[0], gk[1])
		}
		if !r.ShouldUseTerraformPluginFrameworkClient() {
			t.Errorf("%s: not on the plugin-framework client", name)
		}
		if !r.ExternalName.DisableNameInitializer {
			t.Errorf("%s: external name would be initialised from metadata.name and read as 'exists'", name)
		}
		if r.TerraformPluginFrameworkIsStateEmptyFn == nil {
			t.Errorf("%s: no id-based IsStateEmptyFn; first Observe fails with 'cannot find id in tfstate'", name)
		}
		if !r.UseAsync {
			t.Errorf("%s: not async; Create/Update dial apid and must not block the reconciler", name)
		}
		for _, f := range append([]string{"on_destroy", "machine_configuration"}, common.WriteOnlyFields...) {
			if _, ok := r.TerraformResource.Schema[f]; ok {
				t.Errorf("%s: field %s must not be generated", name, f)
			}
		}
		if cc, ok := r.TerraformResource.Schema["client_configuration"]; ok {
			res, ok := cc.Elem.(*schema.Resource)
			if !ok {
				t.Fatalf("%s: client_configuration is not a nested block", name)
			}
			for _, f := range []string{"ca_certificate", "client_certificate", "client_key"} {
				if !res.Schema[f].Sensitive {
					t.Errorf("%s: client_configuration.%s must be a secret reference", name, f)
				}
			}
		} else if name != "talos_image_factory_schematic" {
			t.Errorf("%s: client_configuration missing", name)
		}
	}

	kc := pc.Resources["talos_cluster_kubeconfig"].Sensitive.AdditionalConnectionDetailsFn
	got2, err := kc(map[string]any{"kubeconfig_raw": "raw", "kubernetes_client_configuration": map[string]any{"host": "https://10.0.0.10:6443"}})
	if err != nil || string(got2["kubeconfig"]) != "raw" || string(got2["host"]) != "https://10.0.0.10:6443" || len(got2) != 2 {
		t.Errorf("Kubeconfig extra connection keys = %v (err %v), want kubeconfig and host", got2, err)
	}
	// A state rebuilt after a restart has no kubeconfig_raw; emitting an empty
	// key would blank the published Secret.
	if got2, err := kc(map[string]any{}); err != nil || len(got2) != 0 {
		t.Errorf("Kubeconfig extra connection keys for an empty state = %v (err %v), want none", got2, err)
	}
	if len(GetProvider().Resources) != 0 {
		t.Errorf("cluster-scoped provider must generate no managed resources")
	}
}

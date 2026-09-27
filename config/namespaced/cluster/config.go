// Package cluster configures cluster.talos.m.inc4.net.
package cluster

import (
	ujconfig "github.com/crossplane/upjet/v2/pkg/config"

	"github.com/inc4-ops/provider-talos/config/common"
)

// Configure configures the cluster group.
func Configure(p *ujconfig.Provider) {
	p.AddResourceConfigurator("talos_cluster", func(r *ujconfig.Resource) {
		r.ShortGroup = "cluster" // upjet would put a two-word name in group "talos"
		r.Kind = "Cluster"
		common.ClientConfigurationFromSecret(r)
		r.LateInitializer.IgnoredFields = []string{"endpoint", "control_plane_nodes"}
	})
	p.AddResourceConfigurator("talos_cluster_kubeconfig", func(r *ujconfig.Resource) {
		r.ShortGroup = "cluster"
		r.Kind = "Kubeconfig"
		common.ClientConfigurationFromSecret(r)
		r.LateInitializer.IgnoredFields = []string{"endpoint", "certificate_renewal_duration"}
		r.Sensitive.AdditionalConnectionDetailsFn = kubeconfigDetails
	})
}

// kubeconfigDetails adds a neutral "kubeconfig" key next to upjet's
// attribute.kubeconfig_raw. It returns nothing when the value is absent
// (state rebuilt after a restart), so the merge-patch publisher keeps the
// previously written key instead of blanking it.
func kubeconfigDetails(attr map[string]any) (map[string][]byte, error) {
	out := map[string][]byte{}
	if v, ok := attr["kubeconfig_raw"].(string); ok && v != "" {
		out["kubeconfig"] = []byte(v)
	}
	if kcc, ok := attr["kubernetes_client_configuration"].(map[string]any); ok {
		if v, ok := kcc["host"].(string); ok && v != "" {
			out["host"] = []byte(v)
		}
	}
	return out, nil
}

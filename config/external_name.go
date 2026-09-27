package config

import (
	"github.com/crossplane/upjet/v2/pkg/config"
)

// ExternalNameConfigs lists every exposed Terraform resource. All four get
// their id from the provider (constant "machine_configuration_apply", the
// bootstrap node IP, the kubeconfig cluster name, the schematic ID), so the
// external name is IdentifierFromProvider: it stays empty until Create and
// is never initialised from metadata.name. That empty annotation is what the
// id-based TerraformPluginFrameworkIsStateEmptyFn in config/common keys on.
// Never set crossplane.io/external-name on these MRs in a composition: a set
// annotation reads as "already exists" and Create is skipped.
var ExternalNameConfigs = map[string]config.ExternalName{
	"talos_machine_configuration_apply": config.IdentifierFromProvider,
	"talos_cluster":                     config.IdentifierFromProvider,
	"talos_cluster_kubeconfig":          config.IdentifierFromProvider,
	"talos_image_factory_schematic":     config.IdentifierFromProvider,
}

// ExternalNameConfigurations applies the table above.
func ExternalNameConfigurations() config.ResourceOption {
	return func(r *config.Resource) {
		if e, ok := ExternalNameConfigs[r.Name]; ok {
			r.ExternalName = e
		}
	}
}

// ExternalNameConfigured returns the include list (exact-match regexes).
func ExternalNameConfigured() []string {
	l := make([]string, 0, len(ExternalNameConfigs))
	for name := range ExternalNameConfigs {
		l = append(l, name+"$")
	}
	return l
}

// Package machine configures machine.talos.m.inc4.net.
package machine

import (
	ujconfig "github.com/crossplane/upjet/v2/pkg/config"

	"github.com/inc4-ops/provider-talos/config/common"
)

// Configure configures the machine group.
func Configure(p *ujconfig.Provider) {
	p.AddResourceConfigurator("talos_machine_configuration_apply", func(r *ujconfig.Resource) {
		r.ShortGroup = "machine"
		r.Kind = "ConfigurationApply"
		common.ClientConfigurationFromSecret(r)
		// The rendered config (base + patches) holds every cluster CA key.
		// Removing it from the generated schema keeps it out of the CRD and
		// out of the connection-details mapping, so it is never published.
		delete(r.TerraformResource.Schema, "machine_configuration")
		// on_destroy.reset wipes STATE and EPHEMERAL when the MR is deleted.
		// Without the field the upstream Delete is a no-op, so no composition
		// bug or MR deletion can ever reset a node through this provider.
		delete(r.TerraformResource.Schema, "on_destroy")
		r.LateInitializer.IgnoredFields = []string{"endpoint", "apply_mode"}
	})
}

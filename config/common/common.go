// Package common holds resource options shared by every talos kind.
package common

import (
	"context"

	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	ujconfig "github.com/crossplane/upjet/v2/pkg/config"
)

// WriteOnlyFields are the Terraform 1.11+ write-only twins. upjet v2.5.0 has
// no write-only support, so they would become ordinary spec fields; drop them.
var WriteOnlyFields = []string{
	"client_configuration_wo",
	"machine_configuration_input_wo",
	"machine_configuration_wo",
	"kubeconfig_wo",
}

// Defaults applies to every generated talos resource.
func Defaults() ujconfig.ResourceOption {
	return func(r *ujconfig.Resource) {
		for _, f := range WriteOnlyFields {
			delete(r.TerraformResource.Schema, f)
		}
		// Every exposed resource has a Read that returns the prior state
		// unchanged, so "state is null" never means "absent". Absent == no id.
		r.TerraformPluginFrameworkIsStateEmptyFn = IDIsEmpty
		r.UseAsync = true
		// Keep the controller on the CRD version (no annotation round-trips).
		// The field defaults to "", which upjet treats as differing from Version.
		r.ControllerReconcileVersion = r.Version //nolint:staticcheck // deprecated, but upjet v2.5.0 still reads it
	}
}

// ClientConfigurationFromSecret turns all three client_configuration fields
// into secret references, so a composition points at the talosconfig Secret
// synced from 1Password instead of copying certificates into spec.
func ClientConfigurationFromSecret(r *ujconfig.Resource) {
	cc, ok := r.TerraformResource.Schema["client_configuration"]
	if !ok {
		return
	}
	res, ok := cc.Elem.(*schema.Resource)
	if !ok {
		return
	}
	for _, f := range []string{"ca_certificate", "client_certificate", "client_key"} {
		if s, ok := res.Schema[f]; ok {
			s.Sensitive = true
		}
	}
}

// IDIsEmpty reports a Terraform state without a non-empty "id" as absent.
func IDIsEmpty(_ context.Context, v tftypes.Value, _ rschema.Schema) (bool, error) {
	if v.IsNull() || !v.IsKnown() {
		return true, nil
	}
	m := map[string]tftypes.Value{}
	if err := v.As(&m); err != nil {
		return false, err
	}
	id, ok := m["id"]
	if !ok || id.IsNull() || !id.IsKnown() {
		return true, nil
	}
	var s string
	if err := id.As(&s); err != nil {
		return false, err
	}
	return s == "", nil
}

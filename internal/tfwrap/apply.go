package tfwrap

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// applyResource wraps talos_machine_configuration_apply.
//
// Upstream ModifyPlan always plans machine_configuration (Computed,
// Sensitive) as the freshly rendered config. upjet's no-fork client rebuilds
// the prior state after a provider restart from status.atProvider, which
// never holds sensitive outputs, so the prior value is null and the plan
// shows a known diff: every node would be re-applied once per provider
// restart or upgrade. machine_configuration_hash (Computed, not sensitive)
// does survive in status. When the prior hash equals the planned hash the
// rendered config is unchanged, so the wrapper plans machine_configuration
// back to its prior (null) value and no Update is issued.
type applyResource struct {
	inner resource.Resource
}

var (
	_ resource.Resource                   = &applyResource{}
	_ resource.ResourceWithModifyPlan     = &applyResource{}
	_ resource.ResourceWithValidateConfig = &applyResource{}
	_ resource.ResourceWithUpgradeState   = &applyResource{}
)

func (r *applyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	r.inner.Metadata(ctx, req, resp)
}

func (r *applyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	r.inner.Schema(ctx, req, resp)
}

func (r *applyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.inner.Create(ctx, req, resp)
}

func (r *applyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	r.inner.Read(ctx, req, resp)
}

func (r *applyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.inner.Update(ctx, req, resp)
}

func (r *applyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	r.inner.Delete(ctx, req, resp)
}

func (r *applyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	if v, ok := r.inner.(resource.ResourceWithValidateConfig); ok {
		v.ValidateConfig(ctx, req, resp)
	}
}

func (r *applyResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	if u, ok := r.inner.(resource.ResourceWithUpgradeState); ok {
		return u.UpgradeState(ctx)
	}
	return nil
}

func (r *applyResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if m, ok := r.inner.(resource.ResourceWithModifyPlan); ok {
		m.ModifyPlan(ctx, req, resp)
	}
	if resp.Diagnostics.HasError() || req.State.Raw.IsNull() || resp.Plan.Raw.IsNull() {
		return
	}
	var priorCfg, priorHash, plannedHash types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("machine_configuration"), &priorCfg)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("machine_configuration_hash"), &priorHash)...)
	resp.Diagnostics.Append(resp.Plan.GetAttribute(ctx, path.Root("machine_configuration_hash"), &plannedHash)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if priorCfg.IsNull() && !priorHash.IsNull() && !priorHash.IsUnknown() && priorHash.Equal(plannedHash) {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("machine_configuration"), types.StringNull())...)
	}
}

package tfwrap

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// defaultRenewal mirrors the upstream certificate_renewal_duration default.
const defaultRenewal = 720 * time.Hour

// now is swappable in tests.
var now = time.Now

// kubeconfigResource wraps talos_cluster_kubeconfig.
//
// Upstream renews the admin kubeconfig (Talos issues it for 365 days) by
// marking kubeconfig_raw unknown in ModifyPlan. upjet's filteredDiffExists
// drops diffs whose planned value is unknown, so under upjet the renewal
// never fires. Upstream Read is a no-op; the wrapper makes Read report the
// resource as gone once the client certificate is inside the renewal window.
// upjet then calls Create, which fetches a fresh kubeconfig from the node and
// re-publishes the connection Secret (same id, same external name).
type kubeconfigResource struct {
	inner resource.Resource
}

var (
	_ resource.Resource                 = &kubeconfigResource{}
	_ resource.ResourceWithModifyPlan   = &kubeconfigResource{}
	_ resource.ResourceWithUpgradeState = &kubeconfigResource{}
)

func (r *kubeconfigResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	r.inner.Metadata(ctx, req, resp)
}

func (r *kubeconfigResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	r.inner.Schema(ctx, req, resp)
}

func (r *kubeconfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.inner.Create(ctx, req, resp)
}

func (r *kubeconfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.inner.Update(ctx, req, resp)
}

func (r *kubeconfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	r.inner.Delete(ctx, req, resp)
}

func (r *kubeconfigResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if m, ok := r.inner.(resource.ResourceWithModifyPlan); ok {
		m.ModifyPlan(ctx, req, resp)
	}
}

func (r *kubeconfigResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	if u, ok := r.inner.(resource.ResourceWithUpgradeState); ok {
		return u.UpgradeState(ctx)
	}
	return nil
}

func (r *kubeconfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	r.inner.Read(ctx, req, resp)
	if resp.Diagnostics.HasError() || resp.State.Raw.IsNull() {
		return
	}
	var cert, dur types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("kubernetes_client_configuration").AtName("client_certificate"), &cert)...)
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("certificate_renewal_duration"), &dur)...)
	if resp.Diagnostics.HasError() || cert.IsNull() || cert.IsUnknown() || cert.ValueString() == "" {
		return
	}
	window := defaultRenewal
	if !dur.IsNull() && !dur.IsUnknown() && dur.ValueString() != "" {
		if d, err := time.ParseDuration(dur.ValueString()); err == nil {
			window = d
		}
	}
	der, err := base64.StdEncoding.DecodeString(cert.ValueString())
	if err != nil {
		return
	}
	block, _ := pem.Decode(der)
	if block == nil {
		return
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return
	}
	if c.NotAfter.Before(now().Add(window)) {
		resp.State.RemoveResource(ctx)
	}
}

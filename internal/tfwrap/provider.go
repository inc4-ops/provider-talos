// Package tfwrap embeds siderolabs/terraform-provider-talos unmodified and
// wraps two resources where upjet's semantics differ from Terraform's:
// the apply resource (no re-apply after a provider restart) and the
// kubeconfig resource (renewal actually fires). Everything else is passed through.
package tfwrap

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/siderolabs/terraform-provider-talos/pkg/talos"
)

type wrappedProvider struct {
	provider.Provider
}

// New returns the upstream talos provider with the apply resource wrapped.
// It is stateless (the upstream provider struct has no fields), so a single
// instance is shared by all reconcilers.
func New() provider.Provider {
	return &wrappedProvider{Provider: talos.New()}
}

// Resources returns the upstream constructors, wrapping the apply resource.
func (p *wrappedProvider) Resources(ctx context.Context) []func() resource.Resource {
	var md provider.MetadataResponse
	p.Provider.Metadata(ctx, provider.MetadataRequest{}, &md)
	up := p.Provider.Resources(ctx)
	out := make([]func() resource.Resource, 0, len(up))
	for _, f := range up {
		var rmd resource.MetadataResponse
		f().Metadata(ctx, resource.MetadataRequest{ProviderTypeName: md.TypeName}, &rmd)
		inner := f
		switch rmd.TypeName {
		case "talos_machine_configuration_apply":
			out = append(out, func() resource.Resource { return &applyResource{inner: inner()} })
			continue
		case "talos_cluster_kubeconfig":
			out = append(out, func() resource.Resource { return &kubeconfigResource{inner: inner()} })
			continue
		}
		out = append(out, f)
	}
	return out
}

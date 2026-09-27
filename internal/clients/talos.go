// Package clients builds the per-reconcile Terraform setup for the in-process
// talos provider.
package clients

import (
	"context"

	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"github.com/crossplane/upjet/v2/pkg/terraform"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	namespacedv1beta1 "github.com/inc4-ops/provider-talos/apis/namespaced/v1beta1"
)

const (
	errNoProviderConfig  = "no providerConfigRef provided"
	errGetProviderConfig = "cannot get referenced ProviderConfig"
	errTrackUsage        = "cannot track ProviderConfig usage"
)

// TerraformSetupBuilder returns a SetupFn that hands upjet the shared
// plugin-framework provider plus the only provider-level setting,
// image_factory_url. No Terraform CLI, version or registry source is used.
func TerraformSetupBuilder(fw fwprovider.Provider) terraform.SetupFn {
	return func(ctx context.Context, c client.Client, mg resource.Managed) (terraform.Setup, error) {
		mm, ok := mg.(resource.ModernManaged)
		if !ok {
			return terraform.Setup{}, errors.New("only namespaced managed resources are supported")
		}
		spec, err := resolveProviderConfig(ctx, c, mm)
		if err != nil {
			return terraform.Setup{}, errors.Wrap(err, "cannot resolve provider config")
		}
		cfg := map[string]any{}
		if spec.ImageFactoryURL != nil && *spec.ImageFactoryURL != "" {
			cfg["image_factory_url"] = *spec.ImageFactoryURL
		}
		return terraform.Setup{FrameworkProvider: fw, Configuration: cfg}, nil
	}
}

func resolveProviderConfig(ctx context.Context, c client.Client, mg resource.ModernManaged) (*namespacedv1beta1.ProviderConfigSpec, error) {
	ref := mg.GetProviderConfigReference()
	if ref == nil {
		return nil, errors.New(errNoProviderConfig)
	}
	obj, err := c.Scheme().New(namespacedv1beta1.SchemeGroupVersion.WithKind(ref.Kind))
	if err != nil {
		return nil, errors.Wrap(err, "unknown GVK for ProviderConfig")
	}
	pcObj, ok := obj.(client.Object)
	if !ok {
		return nil, errors.New("provider config is not an Object")
	}
	if err := c.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: mg.GetNamespace()}, pcObj); err != nil {
		return nil, errors.Wrap(err, errGetProviderConfig)
	}
	var spec namespacedv1beta1.ProviderConfigSpec
	switch pc := pcObj.(type) {
	case *namespacedv1beta1.ProviderConfig:
		spec = pc.Spec
	case *namespacedv1beta1.ClusterProviderConfig:
		spec = pc.Spec
	default:
		return nil, errors.New("unknown provider config type")
	}
	t := resource.NewProviderConfigUsageTracker(c, &namespacedv1beta1.ProviderConfigUsage{})
	if err := t.Track(ctx, mg); err != nil {
		return nil, errors.Wrap(err, errTrackUsage)
	}
	return &spec, nil
}

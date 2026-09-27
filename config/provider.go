package config

import (
	// embed the Terraform provider schema and scraped docs
	_ "embed"

	ujconfig "github.com/crossplane/upjet/v2/pkg/config"

	"github.com/inc4-ops/provider-talos/config/common"
	"github.com/inc4-ops/provider-talos/config/namespaced/cluster"
	"github.com/inc4-ops/provider-talos/config/namespaced/image"
	"github.com/inc4-ops/provider-talos/config/namespaced/machine"
	"github.com/inc4-ops/provider-talos/internal/tfwrap"
)

const (
	resourcePrefix = "talos"
	modulePath     = "github.com/inc4-ops/provider-talos"

	// RootGroupNamespaced is the API group suffix of every managed resource.
	RootGroupNamespaced = "talos.m.inc4.net"
	// RootGroupCluster only carries the (unused) legacy ProviderConfig; no
	// cluster-scoped managed resource is generated.
	RootGroupCluster = "talos.inc4.net"
)

//go:embed schema.json
var providerSchema string

//go:embed provider-metadata.yaml
var providerMetadata string

// GetProvider returns the cluster-scoped provider configuration. It includes
// no resources on purpose: inc4-platform composes namespaced MRs only, and a
// cluster-scoped MR could reference Secrets in any namespace.
func GetProvider() *ujconfig.Provider {
	pc := ujconfig.NewProvider([]byte(providerSchema), resourcePrefix, modulePath, []byte(providerMetadata),
		ujconfig.WithRootGroup(RootGroupCluster),
		ujconfig.WithShortName("talos"),
		ujconfig.WithIncludeList([]string{}),
		ujconfig.WithTerraformPluginFrameworkIncludeList([]string{}),
		ujconfig.WithFeaturesPackage("internal/features"),
		// no cluster-scoped ProviderConfig either
		ujconfig.WithBasePackages(ujconfig.BasePackages{}),
	)
	pc.ConfigureResources()
	return pc
}

// GetProviderNamespaced returns the namespaced provider configuration.
func GetProviderNamespaced() *ujconfig.Provider {
	pc := ujconfig.NewProvider([]byte(providerSchema), resourcePrefix, modulePath, []byte(providerMetadata),
		ujconfig.WithRootGroup(RootGroupNamespaced),
		ujconfig.WithShortName("talos"),
		// every resource is reconciled in-process by the plugin-framework
		// provider; nothing goes through the Terraform CLI
		ujconfig.WithIncludeList([]string{}),
		ujconfig.WithTerraformPluginFrameworkIncludeList(ExternalNameConfigured()),
		ujconfig.WithTerraformPluginFrameworkProvider(tfwrap.New()),
		ujconfig.WithFeaturesPackage("internal/features"),
		ujconfig.WithDefaultResourceOptions(
			ExternalNameConfigurations(),
			common.Defaults(),
		),
		ujconfig.WithExampleManifestConfiguration(ujconfig.ExampleManifestConfiguration{
			ManagedResourceNamespace: "default",
		}))

	for _, configure := range []func(provider *ujconfig.Provider){
		machine.Configure,
		cluster.Configure,
		image.Configure,
	} {
		configure(pc)
	}

	pc.ConfigureResources()
	return pc
}

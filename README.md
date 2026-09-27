# provider-talos

A Crossplane provider for [Talos Linux](https://www.talos.dev/), owned by inc4.
It is generated with [upjet](https://github.com/crossplane/upjet) v2.5.0 from
[siderolabs/terraform-provider-talos](https://github.com/siderolabs/terraform-provider-talos)
v0.12.0, which runs in-process through the Terraform plugin framework. The image
carries no Terraform binary.

Only namespaced kinds are generated (API version `v1alpha1`):

| Kind | API group | Terraform resource |
|---|---|---|
| `ConfigurationApply` | `machine.talos.m.inc4.net` | `talos_machine_configuration_apply` |
| `Cluster` | `cluster.talos.m.inc4.net` | `talos_cluster` |
| `Kubeconfig` | `cluster.talos.m.inc4.net` | `talos_cluster_kubeconfig` |
| `FactorySchematic` | `image.talos.m.inc4.net` | `talos_image_factory_schematic` |

`ProviderConfig`, `ClusterProviderConfig` and `ProviderConfigUsage` live in
`talos.m.inc4.net/v1beta1`. The ProviderConfig spec is `{imageFactoryURL?}`; it
holds no credentials, because every managed resource references its own Talos
client configuration Secret.

## Developing

`goimports` must be on `PATH`, because the upjet pipeline shells out to it.

```console
git submodule update --init
GOTOOLCHAIN=go1.26.8 make generate   # schema dump (Terraform 1.5.7, dev time only), docs, codegen, CRDs
go test ./...                        # config invariants, tfwrap and offline connector tests
```

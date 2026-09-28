# provider-talos

A Crossplane provider for [Talos Linux](https://www.talos.dev/), owned by inc4.
It is generated with [upjet](https://github.com/crossplane/upjet) v2.5.0 from
[siderolabs/terraform-provider-talos](https://github.com/siderolabs/terraform-provider-talos)
v0.12.0 (Talos machinery v1.14.0), which runs in-process through the Terraform
plugin framework. The image carries no Terraform binary. The package is published
to `ghcr.io/inc4-ops/provider-talos` and requires Crossplane v2.4.0 or later.

It is built for [inc4-platform](https://github.com/inc4-ops/inc4-platform), which
composes these resources for Talos clusters on Hetzner. The rules under
[Composition rules](#composition-rules) are part of the provider's contract: the
provider cannot enforce them, so every composition must.

## Kinds

Only namespaced kinds are generated (API version `v1alpha1`):

| Kind | API group | Terraform resource | What Create does |
|---|---|---|---|
| `ConfigurationApply` | `machine.talos.m.inc4.net` | `talos_machine_configuration_apply` | Applies a base machine config plus `configPatches` to one node |
| `Cluster` | `cluster.talos.m.inc4.net` | `talos_cluster` | Bootstraps etcd on one control-plane node and waits for the Talos layer; a raised `kubernetesVersion` runs upgrade-k8s |
| `Kubeconfig` | `cluster.talos.m.inc4.net` | `talos_cluster_kubeconfig` | Fetches the admin kubeconfig and publishes it as a connection Secret |
| `FactorySchematic` | `image.talos.m.inc4.net` | `talos_image_factory_schematic` | Registers a schematic with the Image Factory; `status.atProvider.id` is the schematic ID |

`ProviderConfig`, `ClusterProviderConfig` and `ProviderConfigUsage` live in
`talos.m.inc4.net/v1beta1`. The ProviderConfig spec is `{imageFactoryURL?}`
(`https://` only; the default is `https://factory.talos.dev`). It holds no
credentials: every managed resource references its own Talos client configuration.

Fields worth knowing:

- `ConfigurationApply.forProvider`: `node` (required), `endpoint`,
  `clientConfiguration.{caCertificate,clientCertificate,clientKey}SecretRef`,
  `machineConfigurationInputSecretRef`, `configPatches[]`, `applyMode`, `timeouts`.
  Status carries `machineConfigurationHash` and `resolvedApplyMode`.
- `Cluster.forProvider`: `node`, `endpoint`, `controlPlaneNodes[]`,
  `kubernetesVersion` (for example `v1.36.5`), `clientConfiguration.*`,
  `timeouts.{create,update}`.
- `Kubeconfig.forProvider`: `node`, `endpoint`, `certificateRenewalDuration`
  (default `720h`), `clientConfiguration.*`. Connection Secret keys:
  `attribute.kubeconfig_raw`, `kubeconfig` (the same bytes), `host`,
  `attribute.kubernetes_client_configuration.client_key` and
  `attribute.client_configuration.*`.
- `FactorySchematic.forProvider.schematic`: the schematic YAML as a string.

All Secret references are `LocalSecretKeySelector`s, so a managed resource can
only read Secrets in its own namespace. There are no cluster-scoped managed
resources. Removed from the upstream schema on purpose: the rendered
`machine_configuration` output (it holds every CA key), `on_destroy` (node reset
on delete), and every write-only `*_wo` field.

Working manifests are in [`examples/`](examples/); `examples/install.yaml` is for
local try-outs only.

## Behaviour

- **Observe never dials a node.** Every upstream Read returns the prior state,
  so a resource exists exactly when it has an id, which the provider records as
  the external name after Create. There is no drift detection: config reaches a
  node only through Create, or through an Update after the spec changed. A
  replaced or re-imaged node needs a new `ConfigurationApply`, which is why
  inc4-platform keys their names by generation.
- **No re-apply after a provider restart.** After a restart upjet rebuilds the
  prior state from `status.atProvider`, which never holds the sensitive rendered
  config, so upstream would plan a diff and re-apply every node. The
  `internal/tfwrap` apply wrapper keeps the plan empty while
  `machineConfigurationHash` is unchanged. A changed patch still produces an
  Update.
- **Kubeconfig renewal works.** Talos issues the admin client certificate for
  365 days. Once it is inside `certificateRenewalDuration` of expiry, the
  `internal/tfwrap` kubeconfig wrapper reports the resource absent, upjet calls
  the idempotent Create, and the Secret is re-published under the same id.
  The talosconfig client certificate that the managed resources use is **not**
  renewed by the provider.
- **Delete is a no-op.** No upstream Delete of these four resources touches a
  node, and `on_destroy` does not exist, so neither deleting a managed resource
  nor a composition bug can reset a node through this provider.
- **Creates are idempotent.** `Cluster` Create retries bootstrap and treats
  `AlreadyExists` as success, so a re-Create after a health-wait timeout is safe.
  All kinds run asynchronously (upjet caps an operation at 1 h); an operation
  interrupted by a pod restart is retried on the next reconcile.

## Composition rules

These must hold in every composition that uses the provider; the provider
itself cannot enforce them.

1. Use `managementPolicies: [Observe, Create, Update]` and
   `providerConfigRef: {kind: ProviderConfig, name: <name>}`.
2. Never set `crossplane.io/external-name`. The provider sets it after Create;
   a preset value makes a missing resource look existing, and Create never runs.
3. Never set `writeConnectionSecretToRef` on `ConfigurationApply` or `Cluster`:
   it would publish `attribute.machine_configuration_input`, which holds every
   CA key. Only `Kubeconfig` publishes a connection Secret.
4. Never put secrets in `configPatches`: they are stored in plain text in etcd.
   Secrets belong in the base config Secret that
   `machineConfigurationInputSecretRef` points at.
5. Do not use `applyMode: staged_if_needing_reboot`: it makes Observe dial the node.
6. Set `Cluster.timeouts.create: 30m`.
7. Keep the Kubernetes component images rendered into `ConfigurationApply` in
   lockstep with `Cluster.kubernetesVersion`. Raising `kubernetesVersion` makes
   the provider run upgrade-k8s (health-gated, one node at a time); render the
   new images only after `status.atProvider.kubernetesVersion` has changed, so a
   re-render never reverts an upgrade.
8. **Connectivity.** The provider pod needs TCP 50000 (apid) to every node, TCP
   6443 to the control-plane private IPs, and egress to `factory.talos.dev:443`
   (or the configured `imageFactoryURL`).
   - Creation needs only 50000.
   - 6443 is for upgrades: on a `kubernetesVersion` raise, upgrade-k8s talks to
     the Kubernetes API at `https://<Cluster.endpoint>:6443`
     (terraform-provider-talos `talos_cluster_resource.go` L447-490; Talos
     `pkg/cluster/kubernetes.go` L80-90). Keep 6443 open in the network policy
     even though creation works without it.
9. **The first apply is sent insecurely.** On Create *and* Update, upstream
   `talosClientOp` (terraform-provider-talos `pkg/talos/util.go` L517-534) first
   calls `Disks()` with `InsecureSkipVerify` and uses that unauthenticated client
   whenever the call succeeds. A configured node rejects the probe, so the client
   falls back to mTLS. A node in maintenance mode, or anything on the path that
   impersonates one, receives the full machine config, including every CA key.
   Never add a control-plane or worker `ConfigurationApply` before the network
   path to that node is verified (inc4-platform gates this on the node's routing
   annotation). A fail-closed Update needs an upstream change and is not in v1.
10. Treat the `Kubeconfig` connection Secret like the talosconfig Secret: besides
    the admin kubeconfig it carries the Talos `os:admin` client credentials
    (`attribute.client_configuration.*`). Never copy it to another namespace or
    cluster.
11. Mint a long-lived talosconfig client certificate for the provider identity
    only. The provider never renews it (see [Behaviour](#behaviour)).

## Installing

Consumers pin the published **index digest**, never the tag:

```yaml
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-talos
spec:
  package: ghcr.io/inc4-ops/provider-talos@sha256:<index digest>
```

The digest is the `Digest:` line of
`docker buildx imagetools inspect ghcr.io/inc4-ops/provider-talos:<version>`.
Run it logged out: if it fails anonymously, the package is not public and the
cluster needs a pull secret. The tag is only a label; a later publish can move it
in the registry, while the digest cannot change.

A `ProviderConfig` with an empty spec is enough; managed resources select it by
`providerConfigRef`:

```yaml
apiVersion: talos.m.inc4.net/v1beta1
kind: ProviderConfig
metadata:
  name: talos
  namespace: example
spec: {}
```

## Developing

`goimports` must be on `PATH`, because the upjet pipeline shells out to it
(`go install golang.org/x/tools/cmd/goimports` installs the version go.mod pins).

```console
git submodule update --init
GOTOOLCHAIN=go1.26.8 make generate   # schema dump (Terraform 1.5.7, dev time only), docs, codegen, CRDs
make test                            # config invariants, tfwrap and offline connector tests
make lint                            # golangci-lint, version pinned in the Makefile
make notice.check                    # NOTICE names every MPL-2.0 module in the binaries
make build                           # image and xpkg for linux on the host CPU architecture
make e2e                             # fresh kind cluster, Crossplane 2.4.0, this provider, a Talos v1.14.1 container
```

`make e2e` needs docker (about 4 GB of memory) and jq; kubectl, talosctl and yq
are pinned downloads. It deletes and recreates its own kind cluster,
`provider-talos-e2e`, keeps its kubeconfig in `.work/e2e/kubeconfig` (it never
changes the current context of `~/.kube/config`), builds and deploys the
provider, and runs `cluster/test/talos-e2e.sh`: insecure first apply, bootstrap,
kubeconfig Secret keys, and no Update call after a provider restart. `make
e2e-talos` runs the script against a running `make local-deploy`, whose kind
cluster is `provider-talos` (the build submodule's shared default, `local-dev`,
is not used). `make e2e.dump` dumps the e2e control plane.

The Talos container needs a Docker host kernel whose overlayfs accepts layers as
file descriptors (`lowerdir+` through `FSCONFIG_SET_FD`). colima's default
Ubuntu 24.04 VM (kernel 6.8) does not: Talos aborts with `failed to set up /etc
overlay` and the script stops at the apid wait with that hint. The e2e-talos CI
job on the ubuntu-24.04 runners (kernel 6.17) is where it is meant to run.

Bumping terraform-provider-talos: change go.mod and `TERRAFORM_PROVIDER_VERSION`
in the Makefile together (`make check-tf-provider-version` enforces it; Renovate
groups them into one PR and never automerges), run `make generate`, and check
that the `internal/tfwrap` tests still pass: the wrappers key on
`machine_configuration_hash`, `kubernetes_client_configuration` and
`certificate_renewal_duration`, and a rename upstream would silently disable
them. Refresh NOTICE (`python3 hack/notice.py list <binary>`) and run `make e2e`.

## CI and releases

`.github/workflows/ci.yml` runs lint, check-diff (regenerates everything and fails
on a diff, then `make notice.check`), unit tests, local-deploy, the Talos e2e and
an artifact build. On pull requests it also reports CRD breaking changes and
native schema version changes against the base branch.

**Workflow files are pushed over SSH.** The gh CLI token has no `workflow` scope,
so an HTTPS push that adds or changes anything under `.github/workflows` is
rejected. Use `git@github.com:inc4-ops/provider-talos.git` as the remote.

The repository rulesets require a pull request on `main` and protect `v*` tags,
so a tag cannot be moved or deleted once pushed. To release `v0.1.0`:

1. Tag the merged `main` commit over SSH:
   `git tag -a v0.1.0 -m "provider-talos v0.1.0" && git push origin v0.1.0`
   (or run the **Tag** workflow from `main`).
2. Run **Publish Provider Package** from the tag with `version` set to the same
   tag: `gh workflow run publish-provider-package.yml --ref v0.1.0 -f version=v0.1.0`.
   It refuses to run from any other ref, then calls
   `crossplane-contrib/provider-workflows` `publish-provider.yml` at the pinned
   commit `451c4dc` with `registry_org: inc4-ops` and `mirror: false`, granting it
   `packages: write`. That workflow uses older, partly tag-pinned actions; the
   exposure is accepted from the upstream template.
3. After the first publish, make the `ghcr.io/inc4-ops/provider-talos` package
   public (or give consumers a `read:packages` pull secret).
4. Record the index digest (see [Installing](#installing)) and pin it in the
   consumer.

Fallback without Actions, from a checkout of the tag, with a token that has
`write:packages`:

```console
echo "$(gh auth token)" | docker login ghcr.io -u <github-user> --password-stdin
make -j2 build.all VERSION=v0.1.0 BRANCH_NAME=main
make publish VERSION=v0.1.0 BRANCH_NAME=main
```

## Licence

Apache-2.0 (see [LICENSE](LICENSE)). The binary embeds unmodified MPL-2.0 modules,
terraform-provider-talos and the Talos machinery among them; [NOTICE](NOTICE)
lists each one with its source.

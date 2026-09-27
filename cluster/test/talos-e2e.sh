#!/usr/bin/env bash
# Real-node e2e for provider-talos. Runs after `make local-deploy` (kind + Crossplane + this provider).
# One Talos container in maintenance mode joins the kind docker network, so the provider pod reaches
# apid :50000 directly, exactly like a fresh Hetzner node reached over the private overlay.
# Covers: insecure first apply, idempotent bootstrap, kubeconfig publication, and the two upjet
# semantics the tfwrap package fixes (no re-apply after a provider restart; secret keys kept).
set -euo pipefail

: "${KUBECTL:?}" "${TALOSCTL:?}"
TALOS_VERSION=${TALOS_VERSION:-v1.14.1}
K8S_VERSION=${K8S_VERSION:-1.37.0}   # talosctl v1.14 default
NS=talos-e2e
CTX=talos-e2e
WORK=$(mktemp -d)
trap 'docker rm -f talos-e2e >/dev/null 2>&1 || true; rm -rf "$WORK"' EXIT

docker rm -f talos-e2e >/dev/null 2>&1 || true
docker run -d --name talos-e2e --hostname talos-e2e-cp-0 --network kind \
  --read-only --privileged --security-opt seccomp=unconfined \
  --mount type=tmpfs,destination=/run --mount type=tmpfs,destination=/system --mount type=tmpfs,destination=/tmp \
  --mount type=volume,destination=/system/state --mount type=volume,destination=/var \
  --mount type=volume,destination=/etc/cni --mount type=volume,destination=/etc/kubernetes \
  --mount type=volume,destination=/usr/libexec/kubernetes --mount type=volume,destination=/opt \
  -e PLATFORM=container "ghcr.io/siderolabs/talos:${TALOS_VERSION}"
NODE_IP=$(docker inspect -f '{{(index .NetworkSettings.Networks "kind").IPAddress}}' talos-e2e)
export NODE_IP K8S_VERSION

# PKI and base config are generated once, outside any controller (inc4-platform keeps them in 1Password).
"$TALOSCTL" gen secrets -o "$WORK/secrets.yaml"
cat > "$WORK/hostdns.yaml" <<YAML
apiVersion: v1alpha1
kind: ResolverConfig
hostDNS:
  enabled: true
  forwardKubeDNSToHost: true
YAML
"$TALOSCTL" gen config "$CTX" "https://${NODE_IP}:6443" --with-secrets "$WORK/secrets.yaml" \
  --talos-version "${TALOS_VERSION%.*}" --kubernetes-version "$K8S_VERSION" \
  --config-patch "@$WORK/hostdns.yaml" --output-types controlplane,talosconfig -o "$WORK"
tc() { yq -r ".contexts.\"$CTX\".$1" "$WORK/talosconfig"; }

"$KUBECTL" create namespace "$NS" --dry-run=client -o yaml | "$KUBECTL" apply -f -
"$KUBECTL" -n "$NS" create secret generic talos-e2e-talosconfig \
  --from-literal=ca_certificate="$(tc ca)" --from-literal=client_certificate="$(tc crt)" --from-literal=client_key="$(tc key)" \
  --dry-run=client -o yaml | "$KUBECTL" apply -f -
"$KUBECTL" -n "$NS" create secret generic talos-e2e-base --from-file=controlplane.yaml="$WORK/controlplane.yaml" \
  --dry-run=client -o yaml | "$KUBECTL" apply -f -

apply() { envsubst < "$1" | "$KUBECTL" apply -f -; }
apply examples/namespaced/providerconfig/providerconfig.yaml
apply examples/namespaced/machine/configurationapply.yaml
"$KUBECTL" -n "$NS" wait configurationapply.machine.talos.m.inc4.net/talos-e2e-cp-0 --for=condition=Ready --timeout=10m
apply examples/namespaced/cluster/cluster.yaml
"$KUBECTL" -n "$NS" wait cluster.cluster.talos.m.inc4.net/talos-e2e --for=condition=Ready --timeout=30m
apply examples/namespaced/cluster/kubeconfig.yaml
"$KUBECTL" -n "$NS" wait kubeconfig.cluster.talos.m.inc4.net/talos-e2e --for=condition=Ready --timeout=10m

keys() { "$KUBECTL" -n "$NS" get secret talos-e2e-kubeconfig -o json | jq -r '.data | keys | join(",")'; }
before=$(keys); echo "kubeconfig secret keys: $before"
for k in attribute.kubeconfig_raw kubeconfig host; do grep -q "$k" <<<"$before" || { echo "missing $k"; exit 1; }; done
"$KUBECTL" -n "$NS" get secret talos-e2e-kubeconfig -o jsonpath='{.data.kubeconfig}' | base64 -d > "$WORK/kubeconfig"
"$KUBECTL" --kubeconfig "$WORK/kubeconfig" get --raw /version

# Restart: the provider must neither re-apply configs nor blank published keys.
SEL=pkg.crossplane.io/provider=provider-talos
"$KUBECTL" -n crossplane-system delete pod -l "$SEL" --wait
for _ in $(seq 60); do "$KUBECTL" -n crossplane-system get pod -l "$SEL" -o name | grep -q . && break; sleep 2; done
"$KUBECTL" -n crossplane-system wait pod -l "$SEL" --for=condition=Ready --timeout=5m
for r in configurationapply.machine.talos.m.inc4.net/talos-e2e-cp-0 cluster.cluster.talos.m.inc4.net/talos-e2e kubeconfig.cluster.talos.m.inc4.net/talos-e2e; do
  "$KUBECTL" -n "$NS" annotate "$r" e2e/poke="$(date +%s)" --overwrite   # force an immediate reconcile
done
sleep 60
pod=$("$KUBECTL" -n crossplane-system get pod -l "$SEL" -o name | head -1)
"$KUBECTL" -n crossplane-system port-forward "$pod" 18080:8080 >/dev/null 2>&1 & pf=$!; sleep 3
# distroless image: no shell or wget inside, so read /metrics through a port-forward
updates=$(curl -s localhost:18080/metrics | awk '/^upjet_resource_ext_api_duration_count\{operation="update"\}/ {print $2}')
kill $pf
echo "update calls after restart: ${updates:-0}"
[ "${updates:-0}" = "0" ] || { echo "provider re-applied after restart"; exit 1; }
after=$(keys); [ "$before" = "$after" ] || { echo "connection secret keys changed: $before -> $after"; exit 1; }
"$KUBECTL" -n "$NS" get configurationapply,cluster.cluster.talos.m.inc4.net,kubeconfig -o wide
echo "talos e2e passed"

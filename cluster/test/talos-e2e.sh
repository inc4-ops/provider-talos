#!/usr/bin/env bash
# Live e2e for provider-talos against a real Talos node.
#
# Runs against a kind cluster with Crossplane and this provider built from source.
# `make e2e` creates a fresh one of its own (kind-provider-talos-e2e, in its own
# kubeconfig), deploys and runs this script; `make e2e-talos` runs it against the running
# `make local-deploy` cluster. One Talos container boots in maintenance mode on the kind
# docker network, so the provider pod dials apid :50000 on the container IP directly, as
# it dials a fresh Hetzner node over the private network.
#
# Covers:
#   - the insecure first apply of ConfigurationApply to a maintenance-mode node;
#   - Cluster bootstrap and its health wait;
#   - the Kubeconfig connection Secret (keys attribute.kubeconfig_raw, kubeconfig and host)
#     and a live `kubectl get --raw /version` with it;
#   - a provider pod restart: no Update call (upjet_resource_ext_api_duration_count
#     {operation="update"} stays 0, no update events), no re-Create, and a byte-identical
#     connection Secret.
#
# Every kubectl call is pinned to the kind context (kind-$KIND_CLUSTER_NAME), never the
# current context. Host requirements: docker and jq, and a Docker host kernel that boots
# a Talos v1.14 container (see the Makefile: colima's default kernel 6.8 does not).
# KUBECTL, TALOSCTL and YQ (mikefarah yq v4) come from the Makefile as pinned downloads.
set -euo pipefail

: "${KUBECTL:?set KUBECTL}" "${TALOSCTL:?set TALOSCTL}"
YQ=${YQ:-yq}
TALOS_VERSION=${TALOS_VERSION:-v1.14.1}
TALOS_IMAGE=${TALOS_IMAGE:-ghcr.io/siderolabs/talos:${TALOS_VERSION}}
K8S_VERSION=${K8S_VERSION:-1.36.5}
KIND_CLUSTER_NAME=${KIND_CLUSTER_NAME:-provider-talos}
KIND_NETWORK=${KIND_EXPERIMENTAL_DOCKER_NETWORK:-kind}
PROVIDER_NS=${CROSSPLANE_NAMESPACE:-crossplane-system}

KCTX=kind-${KIND_CLUSTER_NAME}
KIND_NODE=${KIND_CLUSTER_NAME}-control-plane
PROVIDER_SEL=pkg.crossplane.io/provider=provider-talos
NS=talos-e2e
NODE=talos-e2e     # docker container name
CLUSTER=talos-e2e  # Talos cluster name, also the talosconfig context name
SECRET=talos-e2e-kubeconfig
MR_APPLY=configurationapply.machine.talos.m.inc4.net/talos-e2e-cp-0
MR_CLUSTER=cluster.cluster.talos.m.inc4.net/talos-e2e
MR_KUBECONFIG=kubeconfig.cluster.talos.m.inc4.net/talos-e2e
MRS=("$MR_APPLY" "$MR_CLUSTER" "$MR_KUBECONFIG")
# controller-runtime controller names (managed.ControllerName lower-cases the GVK string)
CONTROLLERS=(
  "managed/machine.talos.m.inc4.net/v1alpha1, kind=configurationapply"
  "managed/cluster.talos.m.inc4.net/v1alpha1, kind=cluster"
  "managed/cluster.talos.m.inc4.net/v1alpha1, kind=kubeconfig"
)
KIND_KUBECONFIG=/tmp/talos-e2e.kubeconfig  # path inside the kind node container

cd "$(dirname "$0")/../.."
WORK=$(mktemp -d)

k() { "$KUBECTL" --context "$KCTX" "$@"; }
log() { printf '==> %s\n' "$*"; }
die() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

dump() {
  set +e
  echo "---- diagnostics"
  k -n "$NS" describe "${MRS[@]}"
  k -n "$NS" get events --sort-by=.lastTimestamp
  k -n "$PROVIDER_NS" logs -l "$PROVIDER_SEL" --tail=300
  echo "---- Talos container log (tail)"
  docker logs --tail 300 "$NODE" 2>&1
}

diagnose=  # set once the preflight passed; before that a dump only adds noise
cleanup() {
  local rc=$?
  if [ "$rc" -ne 0 ] && [ -n "$diagnose" ]; then dump; fi
  docker exec "$KIND_NODE" rm -f "$KIND_KUBECONFIG" >/dev/null 2>&1 || true
  docker rm -fv "$NODE" >/dev/null 2>&1 || true
  rm -rf "$WORK"
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

# ---------------------------------------------------------------- preflight
for tool in docker jq "$YQ" "$TALOSCTL" "$KUBECTL"; do
  command -v "$tool" >/dev/null || die "$tool not found"
done
"$KUBECTL" config get-contexts "$KCTX" >/dev/null 2>&1 \
  || die "kube context $KCTX not found: run 'make local-deploy' first, or 'make e2e' for a self-contained run"
docker inspect "$KIND_NODE" >/dev/null 2>&1 || die "kind node container $KIND_NODE not found"
k wait provider.pkg.crossplane.io/provider-talos --for=condition=Healthy --timeout=5m
diagnose=1

# A previous run leaves its namespace for inspection. The MRs have no Delete policy, so
# deleting them never touches a node.
log "removing leftovers of a previous run"
docker rm -fv "$NODE" >/dev/null 2>&1 || true
k delete namespace "$NS" --ignore-not-found --wait=true --timeout=5m

# ---------------------------------------------------------------- Talos node
# Mounts as in the Talos v1.14.1 docker provisioner (pkg/provision/providers/docker/node.go).
# No USERDATA: the container platform then has no config source and enters maintenance mode.
log "starting $TALOS_IMAGE in maintenance mode on docker network $KIND_NETWORK"
docker run -d --name "$NODE" --hostname talos-e2e-cp-0 --network "$KIND_NETWORK" \
  --read-only --privileged --security-opt seccomp=unconfined \
  --mount type=tmpfs,destination=/run \
  --mount type=tmpfs,destination=/system \
  --mount type=tmpfs,destination=/tmp \
  --mount type=volume,destination=/var \
  --mount type=volume,destination=/system/state \
  --mount type=volume,destination=/opt \
  -e PLATFORM=container "$TALOS_IMAGE" >/dev/null
NODE_IP=$(docker inspect -f "{{(index .NetworkSettings.Networks \"$KIND_NETWORK\").IPAddress}}" "$NODE")
[ -n "$NODE_IP" ] || die "no IP for $NODE on docker network $KIND_NETWORK"
log "node IP $NODE_IP"

# Talos v1.14 hands overlayfs its layers as file descriptors; an older Docker host kernel
# (colima's default 6.8) rejects that and Talos never starts apid. Name that cause.
boot_hint() {
  docker logs "$NODE" > "$WORK/talos-boot.log" 2>&1 || return 0
  if grep -q 'FSCONFIG_SET_FD failed' "$WORK/talos-boot.log"; then
    printf ' (Talos could not mount its overlays: Docker host kernel %s does not accept overlayfs layers by file descriptor; see the Makefile)' \
      "$(docker info -f '{{.KernelVersion}}' 2>/dev/null)"
  fi
}

# Probe from the kind node: the provider pod dials from there, and on macOS the container
# IP is not reachable from the host at all.
log "waiting for apid on $NODE_IP:50000"
up=
for _ in $(seq 90); do
  if docker exec "$KIND_NODE" timeout 2 bash -c "exec 3<>/dev/tcp/$NODE_IP/50000" 2>/dev/null; then
    up=1
    break
  fi
  [ "$(docker inspect -f '{{.State.Running}}' "$NODE")" = true ] || die "the Talos container exited$(boot_hint)"
  sleep 2
done
[ -n "$up" ] || die "apid did not open $NODE_IP:50000 within 3 minutes$(boot_hint)"

# ---------------------------------------------------------------- PKI and base config
# Generated once, outside any controller, as inc4-platform does. Throwaway: it lives in
# $WORK and in the talos-e2e namespace only.
log "generating throwaway PKI and a base config for Kubernetes $K8S_VERSION"
"$TALOSCTL" gen secrets -o "$WORK/secrets.yaml"
# Talos in a container needs host DNS forwarding (Talos docs, local-platforms/docker).
# Talos v1.14 already generates this document; the patch keeps the requirement explicit.
cat > "$WORK/container.yaml" <<'YAML'
apiVersion: v1alpha1
kind: ResolverConfig
hostDNS:
  enabled: true
  forwardKubeDNSToHost: true
YAML
"$TALOSCTL" gen config "$CLUSTER" "https://${NODE_IP}:6443" \
  --with-secrets "$WORK/secrets.yaml" \
  --talos-version "${TALOS_VERSION%.*}" --kubernetes-version "$K8S_VERSION" \
  --with-docs=false --with-examples=false --with-cluster-discovery=false \
  --config-patch "@$WORK/container.yaml" \
  --output-types controlplane,talosconfig --output "$WORK"
"$TALOSCTL" validate --mode container --strict --config "$WORK/controlplane.yaml"

# The talosconfig holds base64 PEM, which is the format client_configuration expects.
for pair in ca_certificate:ca client_certificate:crt client_key:key; do
  value=$("$YQ" -r ".contexts.\"$CLUSTER\".${pair#*:}" "$WORK/talosconfig")
  if [ -z "$value" ] || [ "$value" = null ]; then die "talosconfig has no ${pair#*:}"; fi
  printf '%s' "$value" > "$WORK/${pair%%:*}"
done

k create namespace "$NS"
k -n "$NS" create secret generic talos-e2e-talosconfig \
  --from-file="$WORK/ca_certificate" --from-file="$WORK/client_certificate" --from-file="$WORK/client_key"
k -n "$NS" create secret generic talos-e2e-base --from-file=controlplane.yaml="$WORK/controlplane.yaml"

# ---------------------------------------------------------------- MRs
render() {
  sed -e "s|\${NODE_IP}|${NODE_IP}|g" -e "s|\${K8S_VERSION}|${K8S_VERSION}|g" "$1" > "$WORK/rendered.yaml"
  if grep -E '\$\{[A-Z_]+\}' "$WORK/rendered.yaml"; then die "unrendered variable in $1"; fi
}
apply() { render "$1"; k apply -f "$WORK/rendered.yaml"; }

log "ConfigurationApply: first apply to the maintenance-mode node (insecure by design)"
apply examples/namespaced/providerconfig/providerconfig.yaml
apply examples/namespaced/machine/configurationapply.yaml
k -n "$NS" wait "$MR_APPLY" --for=condition=Ready --timeout=10m

log "Cluster: bootstrap and health wait (timeouts.create is 30m)"
apply examples/namespaced/cluster/cluster.yaml
k -n "$NS" wait "$MR_CLUSTER" --for=condition=Ready --timeout=35m

log "Kubeconfig"
apply examples/namespaced/cluster/kubeconfig.yaml
k -n "$NS" wait "$MR_KUBECONFIG" --for=condition=Ready --timeout=10m

# ---------------------------------------------------------------- connection Secret
secret_data() { k -n "$NS" get secret "$SECRET" -o json | jq -cS '.data'; }
before=$(secret_data)
echo "published keys: $(jq -r 'keys | join(", ")' <<<"$before")"
for key in attribute.kubeconfig_raw kubeconfig host; do
  jq -e --arg k "$key" 'has($k)' <<<"$before" >/dev/null || die "Secret $SECRET has no key $key"
done
jq -e '.kubeconfig == .["attribute.kubeconfig_raw"]' <<<"$before" >/dev/null \
  || die "keys kubeconfig and attribute.kubeconfig_raw differ"
host=$(jq -r '.host | @base64d' <<<"$before")
[ "$host" = "https://${NODE_IP}:6443" ] || die "host is '$host', want https://${NODE_IP}:6443"

# kubectl runs inside the kind node, which shares the docker network with the Talos node.
# Cluster is Ready once the Talos layer is healthy (etcd, apid, kubelet); the
# kube-apiserver static pod can still be pulling its image, so retry for a while.
log "kubectl --kubeconfig <Secret $SECRET> get --raw /version"
jq -r '.kubeconfig | @base64d' <<<"$before" > "$WORK/kubeconfig"
docker cp "$WORK/kubeconfig" "$KIND_NODE:$KIND_KUBECONFIG" >/dev/null
version=
for _ in $(seq 120); do
  if version=$(docker exec "$KIND_NODE" kubectl --kubeconfig "$KIND_KUBECONFIG" \
    --request-timeout=5s get --raw /version 2>"$WORK/version.err"); then
    break
  fi
  version=
  sleep 5
done
[ -n "$version" ] || die "get --raw /version kept failing for 10 minutes: $(cat "$WORK/version.err")"
echo "$version"
docker exec "$KIND_NODE" rm -f "$KIND_KUBECONFIG"

# ---------------------------------------------------------------- provider restart
log "restarting the provider pod"
old=$(k -n "$PROVIDER_NS" get pod -l "$PROVIDER_SEL" -o jsonpath='{.items[*].metadata.name}')
[ -n "$old" ] || die "no provider pod with label $PROVIDER_SEL"
k -n "$PROVIDER_NS" delete pod -l "$PROVIDER_SEL" --wait=true --timeout=3m
pod=
for _ in $(seq 90); do
  pod=$(k -n "$PROVIDER_NS" get pod -l "$PROVIDER_SEL" -o jsonpath='{.items[*].metadata.name}')
  if [ -n "$pod" ] && [ "$pod" != "$old" ] && [ "${pod// /}" = "$pod" ]; then break; fi
  pod=
  sleep 2
done
[ -n "$pod" ] || die "no single replacement provider pod within 3 minutes"
k -n "$PROVIDER_NS" wait "pod/$pod" --for=condition=Ready --timeout=5m
started=$(k -n "$PROVIDER_NS" get "pod/$pod" -o jsonpath='{.status.startTime}')  # API server clock
for r in "${MRS[@]}"; do  # an extra reconcile on top of the startup resync
  k -n "$NS" annotate "$r" talos-e2e.inc4.net/poke="$(date +%s)" --overwrite >/dev/null
done

# /metrics of the new pod through the API server pod proxy (distroless: no shell inside).
scrape() { k get --raw "/api/v1/namespaces/${PROVIDER_NS}/pods/${pod}:8080/proxy/metrics" > "$WORK/metrics"; }
# series 'name{labels}': the sample value, 0 when the series is absent. Label values may
# contain spaces (controller names do), so the value is the first token after the series.
series() {
  awk -v s="$1" 'index($0, s " ") == 1 { split(substr($0, length(s) + 2), f, " "); v = f[1] }
    END { print v + 0 }' "$WORK/metrics"
}
# reconciled CONTROLLER: reconciles of that controller since the pod started that did not
# error. A successful managed reconcile ends in a poll-interval requeue ("requeue_after").
reconciled() {
  local base="controller_runtime_reconcile_total{controller=\"$1\""
  echo $(( $(series "$base,result=\"requeue_after\"}") + $(series "$base,result=\"success\"}") ))
}

log "waiting until the new pod has reconciled all three MRs"
done_all=
for _ in $(seq 60); do
  if scrape 2>/dev/null; then
    done_all=1
    for c in "${CONTROLLERS[@]}"; do
      [ "$(reconciled "$c")" -ge 1 ] || done_all=
    done
    [ -z "$done_all" ] || break
  fi
  sleep 5
done
[ -n "$done_all" ] || die "the restarted provider did not reconcile all three MRs within 5 minutes"
sleep 30  # an Update started by one of those reconciles runs async; let it finish and be counted
scrape

updates=$(series 'upjet_resource_ext_api_duration_count{operation="update"}')
creates=$(series 'upjet_resource_ext_api_duration_count{operation="create"}')
echo "since the restart: $updates update calls, $creates create calls"
[ "$updates" = 0 ] || die "the provider re-applied after the restart ($updates update calls)"
# A state lost in the restart would show up as a re-Create, which re-applies just the same.
[ "$creates" = 0 ] || die "the provider re-created resources after the restart ($creates create calls)"
k -n "$NS" get events -o json > "$WORK/events.json"
# The specs never change in this test, so any update, before or after the restart, is a re-apply.
update_events=$(jq -r '.items[]
  | select(.reason == "UpdatedExternalResource" or .reason == "CannotUpdateExternalResource")
  | "\(.involvedObject.kind)/\(.involvedObject.name): \(.reason): \(.message)"' "$WORK/events.json")
[ -z "$update_events" ] || die "update events in $NS:"$'\n'"$update_events"
# Requests show up as events at once, even while the async call has not finished.
recreate_events=$(jq -r --arg t "$started" '.items[]
  | select(.reason == "CreatedExternalResource" and (.lastTimestamp // .eventTime // "") >= $t)
  | "\(.involvedObject.kind)/\(.involvedObject.name): \(.message)"' "$WORK/events.json")
[ -z "$recreate_events" ] || die "create events after the restart in $NS:"$'\n'"$recreate_events"

after=$(secret_data)
[ "$before" = "$after" ] || die "Secret $SECRET is not byte-identical after the restart (keys before: $(jq -r 'keys | join(",")' <<<"$before"); after: $(jq -r 'keys | join(",")' <<<"$after"))"
for r in "${MRS[@]}"; do
  k -n "$NS" wait "$r" --for=condition=Ready --timeout=2m >/dev/null
  k -n "$NS" wait "$r" --for=condition=Synced --timeout=2m >/dev/null
done
k -n "$NS" get "${MRS[@]}"
log "talos e2e passed"

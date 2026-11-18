# Deployment Guide

## 1. Prerequisites

- Kubernetes cluster (v1.26+) with KubeVirt installed
- `kubectl` configured to access the cluster
- Container build tool (`docker` or `podman`)
- `kustomize` (if deploying via kustomize)
- prometheus-operator CRDs installed (for PrometheusRule and ServiceMonitor)

## 2. Build and push the image

```bash
IMG=<your-registry>/virt-observability-controller:latest

make docker-build IMG=$IMG
make docker-push IMG=$IMG
```

To use podman instead of docker:

```bash
make docker-build docker-push IMG=$IMG CONTAINER_TOOL=podman
```

## 3. Deploy

### Option A: Kustomize

```bash
make deploy IMG=$IMG
```

This runs `kustomize build config/default` and applies it. It creates all RBAC resources, the ServiceAccount, and the Deployment with a `virt-observability-controller-` name prefix in the `kubevirt` namespace.

The overlay does not create that namespace — KubeVirt already owns it, and the
controller has to live there to mount the virt-handler monitoring client
certificate (see [VMStats feature gate](#vmstats-feature-gate)). If KubeVirt
is installed somewhere else, retarget the overlay:

```bash
cd config/default && kustomize edit set namespace <kubevirt-namespace>
```

### Option B: Static manifest

Generate a single install manifest:

```bash
make build-installer IMG=$IMG
```

Then apply it to any cluster:

```bash
kubectl apply -f dist/install.yaml
```

## 4. Configuration

The controller accepts the following flags (set via the Deployment's `args`):

| Flag | Default | Description |
|------|---------|-------------|
| `--metrics-bind-address` | `:8080` | Address the metrics endpoint binds to (e.g. `:8443` for HTTPS, `:8080` for HTTP) |
| `--metrics-secure` | `true` | Serve metrics over HTTPS with authn/authz |
| `--health-probe-bind-address` | `:8081` | Address the health probe endpoint binds to |
| `--leader-elect` | `false` | Enable leader election for HA deployments |
| `--enable-http2` | `false` | Enable HTTP/2 for metrics and webhook servers |
| `--tls-security-profile` | `""` | TLS security profile for the metrics server: `Old`, `Intermediate`, `Modern`, or `Custom` |
| `--tls-min-version` | `""` | Minimum TLS version (e.g., `VersionTLS12`). Only valid with `--tls-security-profile=Custom` |
| `--tls-ciphers` | `""` | Comma-separated OpenSSL cipher names. Only valid with `--tls-security-profile=Custom` |
| `--metrics-allowlist` | `""` | Groups or metric names to expose (see [Selecting what to expose](#selecting-what-to-expose)) |
| `--alerts-allowlist` | `""` | Groups or alert names to include in the PrometheusRule |
| `--recording-rules-allowlist` | `""` | Groups or recording rule names to include in the PrometheusRule |
| `--enable-vmstats` | `false` | Enable the vmstats poller for domain-level VM metrics |
| `--vmstats-port` | `8187` | Port for the virt-handler vmstats endpoint |
| `--vmstats-cert-path` | `""` | Directory containing the vmstats client certificate |
| `--kubevirt-namespace` | own namespace | Namespace KubeVirt is installed in, where the `kubevirt-ca` CA bundle is read from |
| `--metrics-cert-path` | `""` | Directory containing the metrics server TLS certificate |
| `--webhook-cert-path` | `""` | Directory containing the webhook certificate |

The default kustomize overlay sets `--metrics-bind-address=:8443` via `config/default/manager_metrics_patch.yaml`.

### Selecting what to expose

Metrics, recording rules, and alerts are organised into **groups**. A group is
a named bundle of items that belong together, metrics, the recording rules
built on it, and the alerts that fire on those metrics/rules.

Groups whose contents KubeVirt does not publish itself are enabled by default,
so the zero-flag configuration never double-publishes a series. Every other
group is off. `docs/metrics.md` lists each group, its default, and the items
in it.

The three allowlist flags share one grammar:

| Value | Meaning |
|-------|---------|
| `""` (default) | The groups KubeVirt does not publish |
| `all` | Every group |
| `none` | Nothing |
| `a,b` | Exactly the contents of `a` and `b` |
| `+a,-b` | The default set, plus `a`, minus `b` |

#### The default set with `--enable-vmstats`

What KubeVirt publishes is not fixed, so neither is the default set. With
`--enable-vmstats`, `--alerts-allowlist=""` and
`--recording-rules-allowlist=""` select **every** group rather than only the
groups above.

`--enable-vmstats` requires KubeVirt's `VMStatsCollector` feature gate, since
virt-handler answers `/v1/vmstats` with 403 without it. That same gate makes
virt-operator skip the `prometheus-kubevirt-rules` PrometheusRule entirely, and
delete it if it already exists. So in this configuration KubeVirt publishes no
alerts and no recording rules, and there is nothing left to duplicate: holding
the narrow default would leave `VirtAPIDown` and the rest published by nobody.

`--metrics-allowlist` is unaffected. The gate stops the PrometheusRule, not
virt-handler's exporter, so KubeVirt keeps publishing its own metrics and
widening the metrics default would duplicate them.

Each entry is either a group name or an individual item name, so
`--metrics-allowlist=guest-drivers` and
`--metrics-allowlist=kubevirt_vmi_guest_device_driver_timestamp_seconds` are
both valid. Absolute and relative forms cannot be mixed in one value, and an
entry that names neither a known group nor a known item of that type stops
the controller at startup.

Resolution is logged once on startup:

```
groups enabled  guest-agent, guest-os, guest-drivers, vmi-interfaces, vmi-hotplug, inventory
selection resolved  metrics=23 recordingRules=1 alerts=3
```

#### Replacing KubeVirt's monitoring

To use this controller instead of KubeVirt's own monitoring rather than
alongside it, pass `all` to all three flags and disable the corresponding
rules in KubeVirt.

### VMStats feature gate

When `--enable-vmstats` is set, KubeVirt must have the `VMStatsCollector` feature gate enabled:

```bash
kubectl patch kubevirt kubevirt -n kubevirt --type merge -p \
  '{"spec":{"configuration":{"developerConfiguration":{"featureGates":["VMStatsCollector"]}}}}'
```

The controller also needs access to the virt-handler monitoring client certificate. Mount the `kubevirt-virt-handler-monitoring-client-certs` secret and pass its path via `--vmstats-cert-path` (the default kustomize deployment handles this automatically).

The connection is mutually authenticated: virt-handler requires a client certificate with the `monitoring` common name, and the controller verifies the virt-handler server certificate against the KubeVirt CA bundle in the `kubevirt-ca` ConfigMap.

That client certificate secret can only be mounted from the controller's own namespace, so the controller has to run in the KubeVirt namespace for vmstats to work, and the CA bundle is read from there too. `--kubevirt-namespace` overrides it if the secret reaches the pod some other way.

## 5. Self-managed resources

The controller automatically reconciles the following resources in its own namespace (discovered via the `POD_NAMESPACE` downward API env var):

- **Service** (`virt-observability-controller-metrics`) — exposes the metrics port, selector matches the controller pods
- **Secret** (`virt-observability-controller-metrics-token`) — long-lived ServiceAccount token for Prometheus bearer auth
- **ServiceMonitor** (`virt-observability-controller`) — configures Prometheus to scrape the metrics endpoint with the correct scheme (HTTP/HTTPS) and bearer token

These resources are created on startup and continuously reconciled. If deleted or modified, the controller restores them to the desired state.

### The metrics Service selector

The Service has to select the controller's own pods. By default it selects
`app.kubernetes.io/name=virt-observability-controller`, which is the label
`config/manager/manager.yaml` puts on the pod template.

A deployer that labels the pods differently would otherwise get a Service with
no endpoints, no scrape target and no error anywhere, so the controller reads
its own pod at startup (`POD_NAME` downward API env var) and checks the label
is really there. If it is not, the selector is derived from that pod's labels
instead, minus `pod-template-hash`, and the controller logs:

```
the metrics Service may not select the controller pods
```

Keep the `app.kubernetes.io/name` label on the pod template to stay on the
default path. If you drop `POD_NAME`, the controller cannot run the check and
falls back to the default selector, so the label becomes mandatory.

### Scrape authorization

With `--metrics-secure` on (the default), the metrics endpoint runs a
SubjectAccessReview for a `get` on the `/metrics` non-resource URL. The
ServiceMonitor authenticates with a token for the controller's **own**
ServiceAccount, so that account is the identity being authorized, and it
needs the permission itself or every scrape is answered with 403.

`config/rbac` grants it with two pieces:

- `metrics_auth_role.yaml` — lets the controller create TokenReviews and SubjectAccessReviews, so it can run the check
- `metrics_reader_role.yaml` plus `metrics_reader_role_binding.yaml` — the `get /metrics` permission, bound to the controller ServiceAccount

Both bindings are part of the default overlay. A deployment that builds its
own RBAC rather than applying `config/rbac` has to grant the same `get` on
`/metrics` to whichever ServiceAccount the ServiceMonitor authenticates as.

## 6. Verify the deployment

Check the controller pods are running:

```bash
kubectl get pods -n kubevirt
```

Check the self-managed Service was created:

```bash
kubectl get svc -n kubevirt virt-observability-controller-metrics
```

Check the ServiceAccount Prometheus authenticates as is allowed to scrape:

```bash
kubectl auth can-i get /metrics \
  --as=system:serviceaccount:kubevirt:virt-observability-controller-controller-manager
```

A `no` here means the `metrics-reader` binding is missing and every scrape
will return 403.

Check the controller logs:

```bash
kubectl logs -n kubevirt deploy/virt-observability-controller-controller-manager
```

Port-forward and query metrics:

```bash
kubectl port-forward -n kubevirt svc/virt-observability-controller-metrics 8443:8443
```

```bash
TOKEN=$(kubectl get secret virt-observability-controller-metrics-token \
  -n kubevirt \
  -o jsonpath='{.data.token}' | base64 -d)

curl -sk -H "Authorization: Bearer $TOKEN" https://localhost:8443/metrics | grep ^kubevirt_ | sort
```

## 7. Uninstall

If deployed with kustomize:

```bash
make undeploy
```

If deployed with the static manifest:

```bash
kubectl delete -f dist/install.yaml
```

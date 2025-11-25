# Nodecore Helm Chart

---

## OCI registry

The chart is available at:

```bash
oci://ghcr.io/drpcorg/nodecore-chart
```

You can install a specific version by passing `--version` to `helm`.

---

## TL;DR

Install or upgrade `nodecore` into the `nodecore` namespace:

```bash
helm upgrade \
  --install nodecore \
  oci://ghcr.io/drpcorg/nodecore-chart \
  --version <VERSION> \
  --namespace nodecore \
  --create-namespace
```

Replace `<VERSION>` with one of the published chart versions.

---

## Prerequisites

- Kubernetes cluster (v1.23+ recommended)
- Helm v3.8.0+

---

## Installation

### Minimal installation

```bash
helm upgrade \
  --install nodecore \
  oci://ghcr.io/drpcorg/nodecore-chart \
  --version <VERSION> \
  --namespace nodecore \
  --create-namespace
```

### Installation with custom values

Create your own `values.yaml`:

```yaml
image:
  repository: drpcorg/nodecore
  tag: "latest"
  pullPolicy: IfNotPresent

replicaCount: 1

service:
  type: ClusterIP
  port: 8080
```

Install with:

```bash
helm upgrade \
  --install nodecore \
  oci://ghcr.io/drpcorg/nodecore-chart \
  --version <VERSION> \
  --namespace nodecore \
  --create-namespace \
  -f values.yaml
```

---

## Uninstalling

To uninstall/delete the `nodecore` release:

```bash
helm uninstall nodecore --namespace nodecore
```

This removes all Kubernetes resources created by the chart.

---

## Configuration

| Key                | Type   | Description                      |
| ------------------ | ------ | -------------------------------- |
| `replicaCount`     | int    | Number of replicas               |
| `image.repository` | string | Container image repository       |
| `image.tag`        | string | Image tag                        |
| `image.pullPolicy` | string | Image pull policy                |
| `service.type`     | string | Kubernetes Service type          |
| `service.port`     | int    | Service port                     |
| `resources`        | object | CPU/memory requests and limits   |
| `nodeSelector`     | object | Node selector for pod scheduling |
| `tolerations`      | list   | Tolerations for pod scheduling   |
| `affinity`         | object | Affinity/anti-affinity rules     |
| `nodecoreConfig`   | object | Nodecore configuration           |

Check [`values.yaml`](./values.yaml) for all configuration options and their default
settings.

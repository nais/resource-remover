# Resource Remover

A Kubernetes mutating admission webhook that removes CPU and memory requests/limits from pods, enabling Node Auto-Provisioner (NAP) to right-size cluster nodes based on actual usage.

## What it does

- Intercepts pod creation via mutating admission webhook
- Removes `resources.requests` and `resources.limits` (CPU and memory) from all containers
- Removes `cluster-autoscaler.kubernetes.io/safe-to-evict: "false"` annotations
- Excludes `kube-system` namespace

## Why remove limits?

Kubernetes auto-sets `requests = limits` if limits exist but requests don't. We must remove both to achieve BestEffort QoS class.

## Skipping workloads

To exclude a workload from resource removal, add this annotation to the pod template:

```yaml
metadata:
  annotations:
    resource-remover.nais.io/skip: "true"
```

## Effects

- Pods become `BestEffort` QoS class
- NAP scales down since there are no requests to satisfy
- Pods are first to be evicted under memory pressure
- Cluster cost drops as nodes consolidate

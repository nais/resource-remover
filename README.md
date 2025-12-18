# Resource Remover

A Kubernetes mutating admission webhook that reduces CPU and memory requests to 1/10 of original values and removes limits, enabling Node Auto-Provisioner (NAP) to right-size cluster nodes based on actual usage.

## What it does

- Intercepts pod creation via mutating admission webhook
- Reduces `resources.requests` (CPU and memory) to 1/10 of original values (min 1m CPU, 1Mi memory)
- Removes `resources.limits` (CPU and memory) from all containers
- Removes `cluster-autoscaler.kubernetes.io/safe-to-evict: "false"` annotations
- Excludes `kube-system` namespace

## Why remove limits?

Removing limits prevents CPU throttling and allows pods to burst when needed.

## Skipping workloads

To exclude a workload from resource modification, add this annotation to the pod template:

```yaml
metadata:
  annotations:
    resource-remover.nais.io/skip: "true"
```

## Effects

- Pods get `Burstable` QoS class (reduced requests, no limits)
- NAP scales down since requests are 90% lower
- Pods can burst beyond their requests when resources are available
- Cluster cost drops as nodes consolidate

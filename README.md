# Resource Remover

A Kubernetes mutating admission webhook that reduces CPU and memory requests to 1/10 of original values, removes limits, and disables HPAs, enabling Node Auto-Provisioner (NAP) to right-size cluster nodes based on actual usage.

## What it does

### Pod Mutations (`/mutate`)
- Intercepts pod creation via mutating admission webhook
- Reduces `resources.requests` (CPU and memory) to 1/10 of original values (min 1m CPU, 1Mi memory)
- Removes `resources.limits` (CPU and memory) from all containers and init containers
- Removes `cluster-autoscaler.kubernetes.io/safe-to-evict: "false"` annotations
- Excludes `kube-system` namespace

### HPA Mutations (`/mutate-hpa`)
- Intercepts HPA creation and updates
- Sets `minReplicas=1` and `maxReplicas=1` to disable autoscaling
- Supports all HPA API versions (v1, v2, v2beta1, v2beta2)
- Excludes `kube-system` namespace

## Why remove limits?

Removing limits prevents CPU throttling and allows pods to burst when needed.

## Why disable HPAs?

With reduced resource requests, HPAs would see high utilization and scale up aggressively. Setting replicas to 1 prevents this and saves resources.

## Skipping workloads

To exclude a pod or HPA from modification, add this annotation:

```yaml
metadata:
  annotations:
    resource-remover.nais.io/skip: "true"
```

For pods, add this to the pod template in your Deployment/StatefulSet/DaemonSet spec.

## Effects

- Pods get `Burstable` QoS class (reduced requests, no limits)
- NAP scales down since requests are 90% lower
- Pods can burst beyond their requests when resources are available
- HPAs are disabled (single replica per workload)
- Cluster cost drops as nodes consolidate

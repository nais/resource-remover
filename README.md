# Resource Remover

A Kubernetes mutating admission webhook that reduces CPU and memory requests to 20% of original values, removes limits, sets replicas to 1, and disables HPAs, enabling Node Auto-Provisioner (NAP) to right-size cluster nodes based on actual usage.

## What it does

### Pod Mutations (`/mutate`)
- Intercepts pod creation via mutating admission webhook
- Reduces `resources.requests` (CPU and memory) to 20% of original values (min 1m CPU, 1Mi memory)
- Removes `resources.limits` (CPU and memory) from all containers and init containers
- Removes `cluster-autoscaler.kubernetes.io/safe-to-evict: "false"` annotations
- Excludes `kube-system` namespace

### HPA Mutations (`/mutate-hpa`)
- Intercepts HPA creation and updates
- Sets `minReplicas=1` and `maxReplicas=1` to disable autoscaling
- Supports all HPA API versions (v1, v2, v2beta1, v2beta2)
- Excludes `kube-system` namespace

### Replica Mutations (`/mutate-replicas`)
- Intercepts Deployment creation and updates
- Sets `replicas=1` to reduce workload count
- Excludes `kube-system` namespace

## Why remove limits?

Removing limits prevents CPU throttling and allows pods to burst when needed.

## Why disable HPAs and set replicas to 1?

With reduced resource requests, HPAs would see high utilization and scale up aggressively. Setting replicas to 1 prevents this and saves resources. The replica mutation ensures workloads stay at 1 replica even without an HPA.

## Why not StatefulSets?

Replicas of a StatefulSet are *not* created equal, so you can't just remove some of them and assume everything works as before.

## Skipping workloads

To exclude a pod, HPA, Deployment, or StatefulSet from modification, add this annotation:

```yaml
metadata:
  annotations:
    resource-remover.nais.io/skip: "true"
```

For pods, add this to the pod template in your Deployment/StatefulSet/DaemonSet spec.

## Effects

- Pods get `Burstable` QoS class (reduced requests, no limits)
- NAP scales down since requests are 80% lower
- Pods can burst beyond their requests when resources are available
- HPAs are disabled (single replica per workload)
- Deployments and StatefulSets run with 1 replica
- Cluster cost drops as nodes consolidate

# Resource Remover

A Kubernetes mutating admission webhook that removes CPU and memory requests/limits from pods, enabling Node Auto-Provisioner (NAP) to right-size cluster nodes based on actual usage.

## What it does

- Intercepts pod creation via mutating admission webhook
- Removes `resources.requests` and `resources.limits` (CPU and memory) from all containers
- Removes `cluster-autoscaler.kubernetes.io/safe-to-evict: "false"` annotations
- Excludes `kube-system` namespace

## Why remove limits?

Kubernetes auto-sets `requests = limits` if limits exist but requests don't. We must remove both to achieve BestEffort QoS class.

## Effects

- Pods become `BestEffort` QoS class
- NAP scales down since there are no requests to satisfy
- Pods are first to be evicted under memory pressure
- Cluster cost drops as nodes consolidate

## Deployment

Deployed via Helm chart through [fasit](https://github.com/nais/fasit).

```bash
helm install resource-remover ./charts -n nais-system
```

## Prerequisites

- cert-manager (for webhook TLS certificates)

## Verify

```bash
# Check webhook is running
kubectl -n nais-system get pods -l app=resource-remover

# Check logs
kubectl -n nais-system logs -l app=resource-remover

# Test - should show empty resources
kubectl run test --image=nginx --dry-run=server -o yaml | grep -A5 resources
```

## Disable

```bash
# Quick disable (keeps deployment)
kubectl delete mutatingwebhookconfiguration resource-remover

# Full removal
helm uninstall resource-remover -n nais-system
```

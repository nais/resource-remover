#!/bin/bash
# cleanup.sh
# Restarts all workloads and triggers HPA reconciliation so the mutating webhook can:
# - Reduce resource requests to 20%
# - Remove limits
# - Disable HPAs (set min/max replicas to 1)

set -euo pipefail

EXCLUDE_NAMESPACES="${EXCLUDE_NAMESPACES:-kube-system}"

echo "=== Workload Restarter (for resource-remover webhook) ==="
echo "Excluding namespaces: $EXCLUDE_NAMESPACES"
echo ""

# Check webhook is running
if ! kubectl get mutatingwebhookconfiguration resource-remover &>/dev/null; then
  echo "ERROR: resource-remover webhook not found. Deploy it first."
  exit 1
fi

webhook_pod=$(kubectl get pods -A -l app.kubernetes.io/name=resource-remover -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
if [ -z "$webhook_pod" ]; then
  webhook_pod=$(kubectl get pods -n nais-system -l app=resource-remover -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
fi
if [ -z "$webhook_pod" ]; then
  echo "WARNING: Could not find resource-remover pod. Webhook may not be running."
fi

# Build exclusion pattern for jq
EXCLUDE_PATTERN=$(echo "$EXCLUDE_NAMESPACES" | tr ',' '|')

# Calculate current resource requests (excluding kube-system)
get_total_requests() {
  kubectl get pods -A -o json | jq --arg exclude "$EXCLUDE_PATTERN" '
    [.items[] | 
     select(.metadata.namespace | test($exclude) | not) |
     .spec.containers[] | 
     {
       cpu: (.resources.requests.cpu // "0"),
       mem: (.resources.requests.memory // "0")
     }] |
    {
      cpu_m: (map(if .cpu == "0" then 0 elif .cpu | endswith("m") then (.cpu | .[:-1] | tonumber) else (.cpu | tonumber * 1000) end) | add),
      mem_mi: (map(if .mem == "0" then 0 elif .mem | endswith("Gi") then (.mem | .[:-2] | tonumber * 1024) elif .mem | endswith("Mi") then (.mem | .[:-2] | tonumber) elif .mem | endswith("M") then (.mem | .[:-1] | tonumber) elif .mem | endswith("Ki") then (.mem | .[:-2] | tonumber / 1024) else 0 end) | add)
    }'
}

# Capture before state
echo "=== Calculating current resource requests ==="
BEFORE=$(get_total_requests)
BEFORE_CPU=$(echo "$BEFORE" | jq -r '.cpu_m // 0')
BEFORE_MEM=$(echo "$BEFORE" | jq -r '.mem_mi // 0')
echo "Current requests: ${BEFORE_CPU}m CPU (~$(echo "scale=1; $BEFORE_CPU / 1000" | bc) cores), ${BEFORE_MEM}Mi memory (~$(echo "scale=1; $BEFORE_MEM / 1024" | bc) GB)"
echo ""

# Show breakdown by namespace
echo "=== Requests by namespace ==="
kubectl get pods -A -o json | jq -r --arg exclude "$EXCLUDE_PATTERN" '
  [.items[] | 
   select(.metadata.namespace | test($exclude) | not) |
   {
     ns: .metadata.namespace,
     cpu: ([.spec.containers[].resources.requests.cpu // "0"] | map(if . == "0" then 0 elif endswith("m") then (.[:-1] | tonumber) else (tonumber * 1000) end) | add)
   }] |
  group_by(.ns) |
  map({namespace: .[0].ns, cpu_m: (map(.cpu) | add)}) |
  map(select(.cpu_m > 0)) |
  sort_by(.cpu_m) | reverse | .[] |
  "  \(.namespace): \(.cpu_m)m"'
echo ""

restart_workloads() {
  local kind=$1
  echo "=== Restarting ${kind}s ==="
  
  kubectl get "$kind" --all-namespaces -o json | jq -r --arg exclude "$EXCLUDE_PATTERN" '
    .items[] | 
    select(.metadata.namespace | test($exclude) | not) |
    "\(.metadata.namespace) \(.metadata.name)"
  ' | while read -r ns name; do
    [ -z "$ns" ] && continue
    echo "  $ns/$name"
    kubectl rollout restart "$kind" -n "$ns" "$name" 2>/dev/null || true
  done
}

# Restart all workload types
restart_workloads "deployment"
restart_workloads "statefulset" 
restart_workloads "daemonset"

# Trigger HPA reconciliation by adding a dummy annotation
echo ""
echo "=== Triggering HPA reconciliation ==="
kubectl get hpa --all-namespaces -o json | jq -r --arg exclude "$EXCLUDE_PATTERN" '
  .items[] | 
  select(.metadata.namespace | test($exclude) | not) |
  "\(.metadata.namespace) \(.metadata.name)"
' | while read -r ns name; do
  [ -z "$ns" ] && continue
  echo "  $ns/$name"
  kubectl annotate hpa -n "$ns" "$name" resource-remover.nais.io/reconcile="$(date +%s)" --overwrite 2>/dev/null || true
done

echo ""
echo "=========================================="
echo "=== SUMMARY ==="
echo "=========================================="
echo "Resources that will be reduced to 20%:"
echo "  CPU: ${BEFORE_CPU}m -> ~$(echo "scale=0; $BEFORE_CPU / 5" | bc)m (~$(echo "scale=2; $BEFORE_CPU / 5000" | bc) cores)"
echo "  Memory: ${BEFORE_MEM}Mi -> ~$(echo "scale=0; $BEFORE_MEM / 5" | bc)Mi (~$(echo "scale=2; $BEFORE_MEM / 5120" | bc) GB)"
echo ""
echo "HPAs will be set to minReplicas=1, maxReplicas=1"
echo "=========================================="
echo ""
echo "Rollout restarts and HPA reconciliation initiated."
echo "Monitor pods: kubectl get pods -A -w"
echo "Monitor HPAs: kubectl get hpa -A"
echo "Check reduced requests: kubectl top pods -A --sort-by=cpu | head -20"

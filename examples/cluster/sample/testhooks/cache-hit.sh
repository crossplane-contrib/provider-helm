#!/usr/bin/env bash
set -euo pipefail

# The chart content cache must serve digest-pinned re-pulls without touching
# the registry. Changing a value forces an upgrade, whose chart pull is
# guaranteed sequential after the install that populated the cache; the
# provider logs the hit at Info level.

RESOURCE="release.helm.crossplane.io/cache-hit-cluster"

${KUBECTL} patch "${RESOURCE}" --type=merge -p '{"spec":{"forProvider":{"values":{"ui":{"message":"cache-hit-check"}}}}}'

for _ in $(seq 1 60); do
  revision=$(${KUBECTL} get "${RESOURCE}" -o jsonpath='{.status.atProvider.revision}')
  if [ "${revision:-1}" -ge 2 ]; then
    break
  fi
  sleep 5
done
if [ "${revision:-1}" -lt 2 ]; then
  echo "ERROR: release was not upgraded after values change"
  exit 1
fi

DEPLOY=$(${KUBECTL} -n crossplane-system get deploy -o name | grep provider-helm)
# grep -c instead of -q: -q exits on the first match and SIGPIPEs the still-
# streaming kubectl logs, which pipefail then reports as a pipeline failure.
hits=$(${KUBECTL} -n crossplane-system logs "${DEPLOY}" --tail=-1 | grep -c "chart served from content cache" || true)
if [ "${hits:-0}" -ge 1 ]; then
  echo "content cache hit confirmed in provider logs (helm revision ${revision}, ${hits} hits)"
else
  echo "ERROR: no content cache hit logged for the upgrade re-pull"
  exit 1
fi

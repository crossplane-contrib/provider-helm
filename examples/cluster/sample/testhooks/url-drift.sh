#!/usr/bin/env bash
set -euo pipefail

# Changing spec.forProvider.chart.url must be detected as drift via the
# release.helm.crossplane.io/url-hash label written at deploy time, and must
# roll the release to the chart version the new URL pins.

RESOURCE="release.helm.crossplane.io/url-drift-cluster"
NEW_URL="oci://ghcr.io/stefanprodan/charts/podinfo:6.10.2"

deployed_version() {
  ${KUBECTL} get "${RESOURCE}" -o jsonpath='{.status.atProvider.version}'
}

echo "initial deployed chart version: $(deployed_version)"
[ "$(deployed_version)" = "6.10.1" ]

SECRET=$(${KUBECTL} -n crossplane-system get secret -l "name=url-drift-cluster,owner=helm" --sort-by=.metadata.creationTimestamp -o name | tail -n 1)
URL_HASH=$(${KUBECTL} -n crossplane-system get "${SECRET}" -o jsonpath='{.metadata.labels.release\.helm\.crossplane\.io/url-hash}')
echo "release secret url-hash label: ${URL_HASH}"
[ -n "${URL_HASH}" ]

${KUBECTL} patch "${RESOURCE}" --type=merge -p "{\"spec\":{\"forProvider\":{\"chart\":{\"url\":\"${NEW_URL}\"}}}}"

for _ in $(seq 1 60); do
  if [ "$(deployed_version)" = "6.10.2" ]; then
    revision=$(${KUBECTL} get "${RESOURCE}" -o jsonpath='{.status.atProvider.revision}')
    echo "upgraded to 6.10.2 (helm revision ${revision})"
    [ "${revision}" -ge 2 ]
    exit 0
  fi
  sleep 5
done

echo "ERROR: release was not upgraded after chart URL change"
exit 1

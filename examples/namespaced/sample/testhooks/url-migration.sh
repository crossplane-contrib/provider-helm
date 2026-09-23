#!/usr/bin/env bash
set -euo pipefail

# A release deployed in repository mode carries an empty url-hash label, which
# distinguishes it from a release deployed before label support. Setting
# spec.forProvider.chart.url on it afterwards must be detected as drift against
# that label and roll the release to the chart the URL pins, even though name
# and version are not compared in URL mode.

RESOURCE="release.helm.m.crossplane.io/url-migration-namespaced"
NEW_URL="oci://ghcr.io/stefanprodan/charts/podinfo:6.10.2"

deployed_version() {
  ${KUBECTL} -n crossplane-system get "${RESOURCE}" -o jsonpath='{.status.atProvider.version}'
}

echo "initial deployed chart version: $(deployed_version)"
[ "$(deployed_version)" = "6.10.1" ]

# The url-hash label key must exist on the release Secret, with an empty value.
SECRET=$(${KUBECTL} -n crossplane-system get secret -l "name=url-migration-namespaced,owner=helm,release.helm.crossplane.io/url-hash" --sort-by=.metadata.creationTimestamp -o name | tail -n 1)
echo "release secret carrying the url-hash label: ${SECRET}"
[ -n "${SECRET}" ]
URL_HASH=$(${KUBECTL} -n crossplane-system get "${SECRET}" -o jsonpath='{.metadata.labels.release\.helm\.crossplane\.io/url-hash}')
[ -z "${URL_HASH}" ]

${KUBECTL} -n crossplane-system patch "${RESOURCE}" --type=merge -p "{\"spec\":{\"forProvider\":{\"chart\":{\"url\":\"${NEW_URL}\"}}}}"

for _ in $(seq 1 60); do
  if [ "$(deployed_version)" = "6.10.2" ]; then
    revision=$(${KUBECTL} -n crossplane-system get "${RESOURCE}" -o jsonpath='{.status.atProvider.revision}')
    echo "upgraded to 6.10.2 (helm revision ${revision})"
    [ "${revision}" -ge 2 ]
    exit 0
  fi
  sleep 5
done

echo "ERROR: release was not upgraded after chart URL was set"
exit 1

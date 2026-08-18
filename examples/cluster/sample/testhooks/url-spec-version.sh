#!/usr/bin/env bash
set -euo pipefail

# A bare OCI chart URL must honor spec.forProvider.chart.version instead of
# silently deploying the latest tag.

RESOURCE="release.helm.crossplane.io/url-spec-version-cluster"

got=$(${KUBECTL} get "${RESOURCE}" -o jsonpath='{.status.atProvider.version}')
echo "deployed chart version: ${got}"
[ "${got}" = "6.10.0" ]

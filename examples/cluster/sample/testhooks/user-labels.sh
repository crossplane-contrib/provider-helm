#!/usr/bin/env bash
set -euo pipefail

# spec.forProvider.labels must reach the release Secret, where helm list
# --selector finds them, next to the provider's own labels.

SECRET=$(${KUBECTL} -n crossplane-system get secret -l "name=example-labels,owner=helm,team=platform" --sort-by=.metadata.creationTimestamp -o name | tail -n 1)
echo "release secret selected by team=platform: ${SECRET}"
[ -n "${SECRET}" ]

got=$(${KUBECTL} -n crossplane-system get "${SECRET}" -o jsonpath='{.metadata.labels.example\.com/tier}')
echo "release secret example.com/tier label: ${got}"
[ "${got}" = "backend" ]

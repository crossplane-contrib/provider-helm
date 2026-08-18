#!/usr/bin/env bash
set -euo pipefail

# Digest-pinned deploys must record the deployed digest observably: in
# status.atProvider.digest and as a digest-hash label on the release Secret,
# so the fact survives the status reset after Create and re-creation of the
# managed resource.

RESOURCE="release.helm.m.crossplane.io/example-oci-digest"
DIGEST="sha256:c56f4d760bc9da702f231f37fcec89c66b0993f0cb91446f86d014b133c6693f"
HASH="${DIGEST#sha256:}"
EXPECT_LABEL="sha256-$(printf '%s' "${HASH}" | cut -c1-56)"

got=$(${KUBECTL} -n crossplane-system get "${RESOURCE}" -o jsonpath='{.status.atProvider.digest}')
echo "status.atProvider.digest: ${got}"
[ "${got}" = "${DIGEST}" ]

SECRET=$(${KUBECTL} -n crossplane-system get secret -l "name=example-oci-digest,owner=helm" --sort-by=.metadata.creationTimestamp -o name | tail -n 1)
got=$(${KUBECTL} -n crossplane-system get "${SECRET}" -o jsonpath='{.metadata.labels.release\.helm\.crossplane\.io/digest-hash}')
echo "release secret digest-hash label: ${got}"
[ "${got}" = "${EXPECT_LABEL}" ]

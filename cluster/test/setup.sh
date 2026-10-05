#!/usr/bin/env bash
set -aeuo pipefail

echo "Running setup.sh"

echo "Creating the provider config with cluster admin permissions in cluster..."
SA=$(${KUBECTL} -n crossplane-system get sa -o name | grep provider-helm | sed -e 's|serviceaccount\/|crossplane-system:|g')
${KUBECTL} create clusterrolebinding provider-helm-admin-binding --clusterrole cluster-admin --serviceaccount="${SA}" --dry-run=client -o yaml | ${KUBECTL} apply -f -

echo "Creating a default provider config"
cat <<EOF | ${KUBECTL} apply -f -
apiVersion: helm.crossplane.io/v1beta1
kind: ProviderConfig
metadata:
  name: helm-provider
spec:
  credentials:
    source: InjectedIdentity
EOF

echo "Creating a default provider config (v2)..."
cat <<EOF | ${KUBECTL} apply -f -
apiVersion: helm.m.crossplane.io/v1beta1
kind: ClusterProviderConfig
metadata:
  name: helm-provider-cluster
  namespace: crossplane-system
spec:
  credentials:
    source: InjectedIdentity
EOF

echo "Verifying CEL validation rejects never-valid chart specs..."

# expect_cel_rejection applies the manifest on stdin with a server-side
# dry-run and requires the API server to reject it with the given CEL rule
# message. Asserting on the message, not just on a non-zero exit, keeps a
# transient failure (CRD not yet established, RBAC, connectivity) from passing
# as a validation rejection.
expect_cel_rejection() {
  local what="$1" expected_message="$2" output
  if output=$(${KUBECTL} apply --dry-run=server -f - 2>&1); then
    echo "ERROR: ${what} was not rejected"
    exit 1
  fi
  if ! grep -qF -- "${expected_message}" <<<"${output}"; then
    echo "ERROR: ${what} was rejected for an unexpected reason:"
    echo "${output}"
    exit 1
  fi
  echo "rejected as expected: ${what}"
}

MSG_NAME_REPOSITORY_REQUIRED="chart name and repository are required when url is not set"
MSG_DIGEST_NEEDS_OCI="digest is only supported for OCI registries"

for SCOPE in cluster namespaced; do
  if [ "${SCOPE}" = "cluster" ]; then
    API_VERSION="helm.crossplane.io/v1beta1"
    NAMESPACE_LINE=""
    PROVIDER_CONFIG_NAME="helm-provider"
    PROVIDER_CONFIG_KIND_LINE=""
  else
    API_VERSION="helm.m.crossplane.io/v1beta1"
    NAMESPACE_LINE="namespace: crossplane-system"
    PROVIDER_CONFIG_NAME="helm-provider-cluster"
    PROVIDER_CONFIG_KIND_LINE="kind: ClusterProviderConfig"
  fi

  expect_cel_rejection "${SCOPE} chart spec without url and repository" "${MSG_NAME_REPOSITORY_REQUIRED}" <<MANIFEST
apiVersion: ${API_VERSION}
kind: Release
metadata:
  name: cel-reject-missing-repository
  ${NAMESPACE_LINE}
spec:
  forProvider:
    chart:
      name: podinfo
    namespace: default
  providerConfigRef:
    name: ${PROVIDER_CONFIG_NAME}
    ${PROVIDER_CONFIG_KIND_LINE}
MANIFEST

  expect_cel_rejection "${SCOPE} digest on a non-OCI repository" "${MSG_DIGEST_NEEDS_OCI}" <<MANIFEST
apiVersion: ${API_VERSION}
kind: Release
metadata:
  name: cel-reject-non-oci-digest
  ${NAMESPACE_LINE}
spec:
  forProvider:
    chart:
      name: podinfo
      repository: https://charts.example.com
      digest: sha256:c56f4d760bc9da702f231f37fcec89c66b0993f0cb91446f86d014b133c6693f
    namespace: default
  providerConfigRef:
    name: ${PROVIDER_CONFIG_NAME}
    ${PROVIDER_CONFIG_KIND_LINE}
MANIFEST

  expect_cel_rejection "${SCOPE} digest on a non-OCI url despite an OCI repository" "${MSG_DIGEST_NEEDS_OCI}" <<MANIFEST
apiVersion: ${API_VERSION}
kind: Release
metadata:
  name: cel-reject-digest-with-non-oci-url
  ${NAMESPACE_LINE}
spec:
  forProvider:
    chart:
      url: https://charts.example.com/podinfo-6.10.0.tgz
      repository: oci://ghcr.io/stefanprodan/charts
      digest: sha256:c56f4d760bc9da702f231f37fcec89c66b0993f0cb91446f86d014b133c6693f
    namespace: default
  providerConfigRef:
    name: ${PROVIDER_CONFIG_NAME}
    ${PROVIDER_CONFIG_KIND_LINE}
MANIFEST
done
echo "CEL validation rejects never-valid chart specs as expected"

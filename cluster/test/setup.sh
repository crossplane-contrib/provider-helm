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
if ${KUBECTL} apply --dry-run=server -f - >/dev/null 2>&1 <<MANIFEST
apiVersion: helm.crossplane.io/v1beta1
kind: Release
metadata:
  name: cel-reject-missing-repository
spec:
  forProvider:
    chart:
      name: podinfo
    namespace: default
  providerConfigRef:
    name: helm-provider
MANIFEST
then
  echo "ERROR: chart spec without url and repository was not rejected"
  exit 1
fi

if ${KUBECTL} apply --dry-run=server -f - >/dev/null 2>&1 <<MANIFEST
apiVersion: helm.crossplane.io/v1beta1
kind: Release
metadata:
  name: cel-reject-non-oci-digest
spec:
  forProvider:
    chart:
      name: podinfo
      repository: https://charts.example.com
      digest: sha256:c56f4d760bc9da702f231f37fcec89c66b0993f0cb91446f86d014b133c6693f
    namespace: default
  providerConfigRef:
    name: helm-provider
MANIFEST
then
  echo "ERROR: digest on a non-OCI repository was not rejected"
  exit 1
fi
echo "CEL validation rejects never-valid chart specs as expected"

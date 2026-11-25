#!/usr/bin/env bash
set -euo pipefail

# spec.forProvider.labels must reach the release Secret, where helm list
# --selector finds them. A label removed from the spec must be removed from
# the release, while a label anything else put on the release is kept and
# doesn't count as drift.

RELEASE="example-labels-namespaced"
RESOURCE="release.helm.m.crossplane.io/example-labels-namespaced"

revision() {
  ${KUBECTL} -n crossplane-system get "${RESOURCE}" -o jsonpath='{.status.atProvider.revision}'
}

# label <revision> <jsonpath key> prints a label of that revision's Secret.
label() {
  ${KUBECTL} -n crossplane-system get secret "sh.helm.release.v1.${RELEASE}.v$1" -o jsonpath="{.metadata.labels.$2}"
}

# markers <revision> counts the provider's user label markers.
markers() {
  ${KUBECTL} -n crossplane-system get secret "sh.helm.release.v1.${RELEASE}.v$1" -o json | grep -c '"release\.helm\.crossplane\.io/user-' || true
}

first=$(revision)
echo "release revision: ${first}"
[ "$(label "${first}" team)" = "platform" ]
[ "$(label "${first}" 'example\.com/tier')" = "backend" ]
[ "$(markers "${first}")" = "2" ]

# Stands for a label an admission policy or kubectl label adds.
${KUBECTL} -n crossplane-system label secret "sh.helm.release.v1.${RELEASE}.v${first}" policy.example.com/scan=passed

${KUBECTL} -n crossplane-system patch "${RESOURCE}" --type=merge -p '{"spec":{"forProvider":{"labels":{"team":"infra","example.com/tier":null}}}}'

for _ in $(seq 1 60); do
  current=$(revision)
  if [ "${current}" -gt "${first}" ] && [ "$(label "${current}" team)" = "infra" ]; then
    break
  fi
  sleep 5
done
echo "release revision after the label change: ${current}"
if [ "$(label "${current}" team)" != "infra" ]; then
  echo "ERROR: release was not upgraded after the label change"
  exit 1
fi
[ -z "$(label "${current}" 'example\.com/tier')" ]
[ "$(markers "${current}")" = "1" ]
[ "$(label "${current}" 'policy\.example\.com/scan')" = "passed" ]

# The kept label is no drift: a reconcile forced by an annotation finds the
# release up to date instead of upgrading it again.
${KUBECTL} -n crossplane-system annotate --overwrite "${RESOURCE}" example.com/reconcile-trigger="$(date +%s)"
${KUBECTL} -n crossplane-system wait "${RESOURCE}" --for=condition=Ready --timeout=2m
[ "$(revision)" = "${current}" ]

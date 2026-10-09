# Make takeOwnership a standing claim, and detect ownership drift

## Summary

`spec.forProvider.takeOwnership` currently adopts a pre-existing release exactly once: after
the first successful deploy it is suppressed by `status.atProvider.ownershipTaken`, and every
later reconcile runs normal Helm validation. That means a resource the release adopted and
another actor subsequently re-stamps is never taken back — nothing in the chart, values or
digest changes, so no upgrade is ever triggered, and Helm only validates ownership for
resources it is about to create.

This PR changes `takeOwnership` from a one-shot migration into a standing claim:

- **While `takeOwnership` is set, every deploy exercises adoption** — the flag is passed
  straight through to Helm's install and upgrade actions instead of being suppressed by
  `ownershipTaken`.
- **Every reconcile scans the release's rendered resources for ownership drift** — if any live
  resource carries ownership metadata that names another actor, the observation reports not
  up to date, which triggers a deploy that re-stamps it.
- **`status.atProvider.ownershipTaken` becomes purely informational** — it records that
  adoption happened; it no longer suppresses it.

## Behavior change

**This is a semantic change for existing releases.** Previously, `takeOwnership: true` was a
one-time adoption: once recorded, chart upgrades and value changes would *not* silently adopt
newly-introduced or unrelated pre-existing resources. Under this change, `takeOwnership: true`
plus any spec change (chart bump, values edit) will deploy with adoption exercised — Helm
adopts every existing resource the chart now renders, without per-resource validation.

This is the intended contract of the field going forward ("keep them there when another actor
re-stamps their ownership metadata"), but anyone who relied on the old one-time semantics
should re-read it. Users who want a one-time migration should still remove `takeOwnership`
from their spec after the adoption deploy — the sticky
`release.helm.crossplane.io/ownership-taken` label and `status.atProvider.ownershipTaken`
keep recording that adoption happened.

Also new: with `takeOwnership` set, each `Observe` performs one API lookup **per rendered
resource**, per reconcile, while no deploy is otherwise owed. To keep this cheap:

- lookups fetch `PartialObjectMetadata` only — just the labels and annotations the ownership
  check compares;
- the check is skipped entirely when `takeOwnership` is unset (opt-in, zero cost), and when a
  deploy is already owed from spec drift (the deploy would adopt everything regardless, so
  the lookups cannot change the observation).

## Drift detection

New `OwnershipDrifted` in `pkg/clients/helm` parses the last release's persisted manifest and
compares each live resource's ownership metadata — `app.kubernetes.io/managed-by`,
`meta.helm.sh/release-name`, `meta.helm.sh/release-namespace` — against what this release
expects, mirroring Helm's own `checkOwnership` (a missing key counts as not owned). Resources
that are absent are not drift (the next deploy creates them); kinds the cluster does not yet
serve (e.g. a CRD the chart also installs) are skipped, as they are in Helm's own adoption
path.

The check runs from `Observe` only when `takeOwnership` is set and no deploy is already owed,
and its result deliberately does **not** touch `status.synced` or the Available condition:
the release still is the chart, values and version that were asked for, and its workloads are
still running. Drift only forces `ResourceUpToDate` to false, so the managed reconciler runs
an `Upgrade`, and Helm's upgrade force-restamps ownership metadata on every rendered
resource — which is what repairs the drift.

A lookup error for a rendered resource fails the observation (wrapped as
`failed to check ownership of release resources`) rather than silently reporting "up to date";
persistent RBAC gaps on such kinds will surface as reconcile errors instead of invisible
drift.

## API changes

All surfaced as comment/description updates — **no new fields** (CRD shapes are unchanged,
regenerated descriptions only):

```yaml
apiVersion: helm.crossplane.io/v1beta1  # or helm.m.crossplane.io for namespaced
kind: Release
spec:
  forProvider:
    chart:
      name: mychart
      repository: https://charts.example.com
      version: 1.2.3
    namespace: default
    takeOwnership: true  # now a standing claim, see above
```

- `spec.forProvider.takeOwnership` — documented as a standing claim, with the interplay with
  `ssaForceConflicts` for contended resources spelled out.
- `status.atProvider.ownershipTaken` — documented as informational only; does not suppress
  further adoption while the spec field is set.

## Implementation

- `pkg/clients/helm/ownership.go` *(new)* — `OwnershipDrifted`: manifest parsing, namespace
  resolution via the client's RESTMapper (cluster-scoped vs namespaced), metadata-only
  lookups, Helm-mirroring ownership check.
- `deployOptions` — passes `TakeOwnership` through unconditionally instead of masking it with
  `!ownershipTaken`; the `ownershipTaken` label/status logic is retained for observability and
  label stickiness, no longer as a gate.
- `Observe` (cluster and namespaced) — new `checkOwnershipDrift` gate
  (`takeOwnership && specUpToDate`) whose result joins `status.synced` in computing
  `ResourceUpToDate`.
- Ownership constants (`OwnerLabelManagedBy`, `OwnerAnnotationReleaseName`,
  `OwnerAnnotationReleaseNamespace`) extracted to the helm client package; controllers now
  reference them.
- Generated CRDs (`package/crds/*.yaml`) reflect the updated field descriptions.

## Tests

- `ownership_test.go` *(new)*: drift/no-drift across all three metadata keys, missing keys,
  absent resources, unserved kinds, `generateName` resources, empty/comment-only manifests,
  malformed manifest error, Get-failure error short-circuit, and correct lookup keys
  (namespace defaulting, explicit namespace, cluster-scoped fetch).
- `Observe` drift tests (cluster and namespaced): foreign stamps force a deploy while status
  stays synced; owned stamps do not; no lookups without `takeOwnership`; **no lookups when
  spec drift already owes a deploy**; lookup failure fails the observation.
- Updated `deployOptions`/`Update` tests: with `takeOwnership` set and adoption already
  recorded, deploys still exercise `TakeOwnership: true` (re-adoption on every deploy).

## Files changed

- `apis/cluster/release/v1beta1/types.go`, `apis/namespaced/release/v1beta1/types.go` — field docs
- `package/crds/helm.crossplane.io_releases.yaml`, `package/crds/helm.m.crossplane.io_releases.yaml` — regenerated descriptions
- `pkg/clients/helm/ownership.go`, `pkg/clients/helm/ownership_test.go` — drift check *(new)*
- `pkg/clients/helm/labels.go` — label comment update
- `pkg/controller/cluster/release/release.go`, `pkg/controller/namespaced/release/release.go` — wiring
- `pkg/controller/{cluster,namespaced}/release/release_test.go` — test updates

## Verification

- `go build ./...`, `go test ./pkg/...`, `golangci-lint run ./pkg/... ./apis/...` — all pass.
- End-to-end on a local kind cluster (kind `local-dev`, Crossplane v2.4.2, locally-built
  provider via `make local-deploy`, chart `podinfo` 6.10.1):
  - **Adoption**: a Service created beforehand with `kubectl` (no Helm metadata) was adopted
    on install with `takeOwnership: true` — `managed-by: Helm` label and both `meta.helm.sh`
    annotations stamped, `ownershipTaken=true`, release `deployed`.
  - **Drift repair**: with the Service re-stamped by a foreign actor (label stripped,
    release-name annotation pointed elsewhere), the next reconcile detected the drift and
    upgraded, restoring the correct ownership metadata — with `ssaForceConflicts: false` the
    upgrade failed loudly on a field-manager conflict instead of stealing the key; with
    `ssaForceConflicts: true` the documented mitigation repaired it. This exercised the full
    loop: observe detects drift → `ResourceUpToDate=false` → upgrade → Helm re-stamps →
    observe clean.
  - **Control**: with `takeOwnership: false`, the same foreign stamp was left untouched and
    no deploy fired — foreign metadata is invisible without opting in.
  - **Steady state**: with `takeOwnership: true` and ownership correct, repeated reconciles
    report "up to date" — no upgrade loop from the standing claim.
  - The repo's stock e2e suite (`make uptest`, 21 example Releases, cluster and namespaced)
    also passes.
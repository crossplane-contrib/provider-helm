package release

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ktypes "sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/yaml"

	"github.com/crossplane-contrib/provider-helm/apis/cluster/release/v1beta1"
)

const (
	keyDefaultPatchFrom        = "patch.yaml"
	errFailedToUnmarshallPatch = "failed to unmarshal patch"
)

// Patcher interface for managing Kustomize patches and detecting updates
type Patcher interface {
	hasUpdates(ctx context.Context, kube client.Client, in []v1beta1.ValueFromSource, s v1beta1.ReleaseStatus) (bool, error)
	patchGetter
	patchHasher
}

type patchGetter interface {
	getFromSpec(ctx context.Context, kube client.Client, vals []v1beta1.ValueFromSource) ([]ktypes.Patch, error)
}

type patchHasher interface {
	shaOf(patches []ktypes.Patch) (string, error)
}

func newPatcher() Patcher {
	return patch{
		patchHasher: patchSha{},
		patchGetter: patchGet{},
	}
}

type patch struct {
	patchHasher
	patchGetter
}

func (p patch) hasUpdates(ctx context.Context, kube client.Client, in []v1beta1.ValueFromSource, s v1beta1.ReleaseStatus) (bool, error) {
	patches, err := p.getFromSpec(ctx, kube, in)
	if err != nil {
		return false, err
	}

	sum, err := p.shaOf(patches)
	if err != nil {
		return false, err
	}

	return !strings.EqualFold(sum, s.PatchesSha), nil
}

type patchSha struct{}

func (patchSha) shaOf(patches []ktypes.Patch) (string, error) {
	if len(patches) == 0 {
		return "", nil
	}

	jb, err := json.Marshal(patches)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", sha256.Sum256(jb)), nil
}

type patchGet struct{}

func (patchGet) getFromSpec(ctx context.Context, kube client.Client, vals []v1beta1.ValueFromSource) ([]ktypes.Patch, error) {
	var base []ktypes.Patch // nolint:prealloc

	for _, vf := range vals {
		s, err := getDataValueFromSource(ctx, kube, vf, keyDefaultPatchFrom)
		if err != nil {
			return nil, errors.Wrap(err, errFailedToGetValueFromSource)
		}

		if s == "" {
			continue
		}

		var p struct {
			Patches []ktypes.Patch `json:"patches"`
		}
		if err = yaml.Unmarshal([]byte(s), &p); err != nil {
			return nil, errors.Wrap(err, errFailedToUnmarshallPatch)
		}
		base = append(base, p.Patches...)
	}

	return base, nil
}

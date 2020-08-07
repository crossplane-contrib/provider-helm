package controller

import (
	"context"

	"github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ktypes "sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/yaml"

	"github.com/crossplane-contrib/provider-helm/apis/v1alpha1"
)

const (
	keyDefaultPatchFrom        = "patch.yaml"
	errFailedToUnmarshallPatch = "failed to unmarshal patch"
)

func getPatchesFromSpec(ctx context.Context, kube client.Client, vals []v1alpha1.ValueFromSource) ([]ktypes.Patch, error) {
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

package helm

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

const (
	kustomizationFileName  = "kustomization.yaml"
	helmOutputFileName     = "helm-output.yaml"
	helmTempDirNamePattern = "helm-post-render"
)

// KustomizationRender Implements helm PostRenderer interface
type KustomizationRender struct {
	patches []types.Patch
	logger  logging.Logger
}

// Run runs a set of Kustomize patches against yaml input and returns the patched content.
func (kr KustomizationRender) Run(renderedManifests *bytes.Buffer) (modifiedManifests *bytes.Buffer, err error) {
	d, err := os.MkdirTemp("", helmTempDirNamePattern)
	if err != nil {
		return nil, err
	}

	fsys := filesys.MakeFsOnDisk()
	defer func() {
		if err := fsys.RemoveAll(d); err != nil {
			kr.logger.Info("Failed to cleanup tmp data", "path", d, "err", err)
		}
	}()

	k := types.Kustomization{
		Resources: []string{helmOutputFileName},
		Patches:   kr.patches,
	}

	kdata, err := json.Marshal(k)
	if err != nil {
		return nil, err
	}

	err = fsys.WriteFile(filepath.Join(d, kustomizationFileName), kdata)
	if err != nil {
		return nil, err
	}

	err = fsys.WriteFile(filepath.Join(d, helmOutputFileName), renderedManifests.Bytes())
	if err != nil {
		return nil, err
	}

	opts := &krusty.Options{
		DoLegacyResourceSort: false,
		LoadRestrictions:     types.LoadRestrictionsRootOnly,
		DoPrune:              false,
		PluginConfig:         types.DisabledPluginConfig(),
	}

	kust := krusty.MakeKustomizer(opts)
	m, err := kust.Run(fsys, d)
	if err != nil {
		return nil, err
	}

	yml, err := m.AsYaml()
	if err != nil {
		return nil, err
	}

	return bytes.NewBuffer(yml), nil
}

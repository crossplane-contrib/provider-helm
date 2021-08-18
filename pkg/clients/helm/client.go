/*
Copyright 2020 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package helm

import (
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/client-go/rest"
	ktype "sigs.k8s.io/kustomize/api/types"

	"github.com/crossplane-contrib/provider-helm/apis/release/v1beta1"
)

const (
	helmDriverSecret  = "secret"
	chartCache        = "/tmp/charts"
	releaseMaxHistory = 20
)

const (
	errFailedToCheckIfLocalChartExists = "failed to check if cached chart file exists"
	errFailedToPullChart               = "failed to pull chart"
	errFailedToLoadChart               = "failed to load chart"
	errUnexpectedDirContentTmpl        = "expected 1 .tgz chart file, got [%s]"
	errFailedToParseURL                = "failed to parse URL"
)

// Client is the interface to interact with Helm
type Client interface {
	GetLastRelease(release string) (*release.Release, error)
	Install(release string, chart *chart.Chart, vals map[string]interface{}, patches []ktype.Patch) (*release.Release, error)
	Upgrade(release string, chart *chart.Chart, vals map[string]interface{}, patches []ktype.Patch) (*release.Release, error)
	Rollback(release string) error
	Uninstall(release string) error
	PullAndLoadChart(spec *v1beta1.ChartSpec, creds *RepoCreds) (*chart.Chart, error)
}

type client struct {
	log             logging.Logger
	pullClient      *action.Pull
	getClient       *action.Get
	installClient   *action.Install
	upgradeClient   *action.Upgrade
	rollbackClient  *action.Rollback
	uninstallClient *action.Uninstall
}

// NewClient returns a new Helm Client with provided config
func NewClient(log logging.Logger, config *rest.Config, namespace string, wait bool, timeout time.Duration) (Client, error) {
	rg := newRESTClientGetter(config, namespace)

	actionConfig := new(action.Configuration)
	// Always store helm state in the same cluster/namespace where chart is deployed
	if err := actionConfig.Init(rg, namespace, helmDriverSecret, func(format string, v ...interface{}) {
		log.Debug(fmt.Sprintf(format, v))
	}); err != nil {
		return nil, err
	}

	pc := action.NewPull()

	if _, err := os.Stat(chartCache); os.IsNotExist(err) {
		err = os.Mkdir(chartCache, 0750)
		if err != nil {
			return nil, err
		}
	}

	pc.DestDir = chartCache
	pc.Settings = &cli.EnvSettings{}

	gc := action.NewGet(actionConfig)

	ic := action.NewInstall(actionConfig)
	ic.Namespace = namespace
	ic.Wait = wait
	ic.Timeout = timeout

	uc := action.NewUpgrade(actionConfig)
	uc.Wait = wait
	uc.Timeout = timeout

	uic := action.NewUninstall(actionConfig)

	rb := action.NewRollback(actionConfig)
	rb.Wait = wait
	rb.Timeout = timeout

	return &client{
		log:             log,
		pullClient:      pc,
		getClient:       gc,
		installClient:   ic,
		upgradeClient:   uc,
		rollbackClient:  rb,
		uninstallClient: uic,
	}, nil
}

func getChartFileName(dir string) (string, error) {
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		return "", err
	}
	if len(files) != 1 {
		fileNames := make([]string, 0, len(files))
		for _, f := range files {
			fileNames = append(fileNames, f.Name())
		}
		return "", errors.Errorf(errUnexpectedDirContentTmpl, strings.Join(fileNames, ","))
	}
	return files[0].Name(), nil
}

// Pulls latest chart version. Returns absolute chartFilePath or error.
func (hc *client) pullLatestChartVersion(spec *v1beta1.ChartSpec, creds *RepoCreds) (string, error) {
	tmpDir, err := ioutil.TempDir(chartCache, "")
	if err != nil {
		return "", err
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			hc.log.WithValues("tmpDir", tmpDir).Info("failed to remove temporary directory")
		}
	}()

	if err := hc.pullChart(spec, creds, tmpDir); err != nil {
		return "", nil
	}

	chartFileName, err := getChartFileName(tmpDir)
	if err != nil {
		return "", err
	}

	chartFilePath := filepath.Join(chartCache, chartFileName)
	if err := os.Rename(filepath.Join(tmpDir, chartFileName), chartFilePath); err != nil {
		return "", nil
	}
	return chartFilePath, nil
}

func (hc *client) pullChart(spec *v1beta1.ChartSpec, creds *RepoCreds, chartDir string) error {
	pc := hc.pullClient

	chartRef := spec.URL
	if spec.URL == "" {
		chartRef = spec.Name

		pc.RepoURL = spec.Repository
		pc.Version = spec.Version
	}
	pc.Username = creds.Username
	pc.Password = creds.Password

	pc.DestDir = chartDir

	o, err := pc.Run(chartRef)
	hc.log.Debug(o)
	if err != nil {
		return errors.Wrap(err, errFailedToPullChart)
	}
	return nil
}

func (hc *client) PullAndLoadChart(spec *v1beta1.ChartSpec, creds *RepoCreds) (*chart.Chart, error) {
	var chartFilePath string
	var err error
	if spec.URL == "" && spec.Version == "" {
		chartFilePath, err = hc.pullLatestChartVersion(spec, creds)
		if err != nil {
			return nil, err
		}
	} else {
		filename := fmt.Sprintf("%s-%s.tgz", spec.Name, spec.Version)
		if spec.URL != "" {
			u, err := url.Parse(spec.URL)
			if err != nil {
				return nil, errors.Wrap(err, errFailedToParseURL)
			}
			filename = path.Base(u.Path)
		}
		chartFilePath = filepath.Join(chartCache, filename)

		if _, err := os.Stat(chartFilePath); os.IsNotExist(err) {
			if err = hc.pullChart(spec, creds, chartCache); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, errors.Wrap(err, errFailedToCheckIfLocalChartExists)
		}
	}

	chart, err := loader.Load(chartFilePath)
	if err != nil {
		return nil, errors.Wrap(err, errFailedToLoadChart)
	}
	return chart, nil
}

func (hc *client) GetLastRelease(release string) (*release.Release, error) {
	return hc.getClient.Run(release)
}

func (hc *client) Install(release string, chart *chart.Chart, vals map[string]interface{}, patches []ktype.Patch) (*release.Release, error) {
	hc.installClient.ReleaseName = release

	if len(patches) > 0 {
		hc.installClient.PostRenderer = &KustomizationRender{
			patches: patches,
			logger:  hc.log,
		}
	}

	return hc.installClient.Run(chart, vals)
}

func (hc *client) Upgrade(release string, chart *chart.Chart, vals map[string]interface{}, patches []ktype.Patch) (*release.Release, error) {
	// Reset values so that source of truth for desired state is always the CR itself
	hc.upgradeClient.ResetValues = true
	hc.upgradeClient.MaxHistory = releaseMaxHistory

	if len(patches) > 0 {
		hc.upgradeClient.PostRenderer = &KustomizationRender{
			patches: patches,
			logger:  hc.log,
		}
	}

	return hc.upgradeClient.Run(release, chart, vals)
}

func (hc *client) Rollback(release string) error {
	return hc.rollbackClient.Run(release)
}

func (hc *client) Uninstall(release string) error {
	_, err := hc.uninstallClient.Run(release)
	return err
}

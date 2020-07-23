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

package helmClient

import (
	"fmt"
	"os"
	"path/filepath"

	"helm.sh/helm/v3/pkg/cli"

	"k8s.io/client-go/rest"

	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/release"
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
)

type Client interface {
	GetLastRelease(release string) (*release.Release, error)
	Install(release string, chartDef ChartDefinition, vals map[string]interface{}) (*release.Release, error)
	Upgrade(release string, chartDef ChartDefinition, vals map[string]interface{}) (*release.Release, error)
	Rollback(release string) error
	Uninstall(release string) error
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

func NewClient(log logging.Logger, config *rest.Config, namespace string) (Client, error) {
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
	ic.CreateNamespace = true

	uc := action.NewUpgrade(actionConfig)
	uic := action.NewUninstall(actionConfig)

	rb := action.NewRollback(actionConfig)

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

func (hc *client) pullAndLoadChart(repo, name, version, username, password string) (*chart.Chart, error) {
	pc := hc.pullClient

	pc.RepoURL = repo
	pc.Version = version
	pc.Username = username
	pc.Password = password

	df := filepath.Join(pc.DestDir, fmt.Sprintf("%s-%s.tgz", name, version))
	if _, err := os.Stat(df); os.IsNotExist(err) {
		o, err := pc.Run(name)
		hc.log.Debug(o)
		if err != nil {
			return nil, errors.Wrap(err, errFailedToPullChart)
		}
	} else if err != nil {
		return nil, errors.Wrap(err, errFailedToCheckIfLocalChartExists)
	}

	chart, err := loader.Load(df)
	if err != nil {
		return nil, errors.Wrap(err, errFailedToLoadChart)
	}
	return chart, nil
}

func (hc *client) GetLastRelease(release string) (*release.Release, error) {
	return hc.getClient.Run(release)
}

func (hc *client) Install(release string, chartDef ChartDefinition, vals map[string]interface{}) (*release.Release, error) {
	hc.installClient.ReleaseName = release

	c, err := hc.pullAndLoadChart(chartDef.Repository, chartDef.Name, chartDef.Version, chartDef.RepoUser, chartDef.RepoPass)
	if err != nil {
		return nil, err
	}

	return hc.installClient.Run(c, vals)
}

func (hc *client) Upgrade(release string, chartDef ChartDefinition, vals map[string]interface{}) (*release.Release, error) {
	// Reset values so that source of truth for desired state is always the CR itself
	hc.upgradeClient.ResetValues = true
	hc.upgradeClient.MaxHistory = releaseMaxHistory

	c, err := hc.pullAndLoadChart(chartDef.Repository, chartDef.Name, chartDef.Version, chartDef.RepoUser, chartDef.RepoPass)
	if err != nil {
		return nil, err
	}
	return hc.upgradeClient.Run(release, c, vals)
}

func (hc *client) Rollback(release string) error {
	return hc.rollbackClient.Run(release)
}

func (hc *client) Uninstall(release string) error {
	_, err := hc.uninstallClient.Run(release)
	return err
}

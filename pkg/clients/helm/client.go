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

	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/registry"
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
	errFailedToLogin                   = "failed to login to registry"
	errUnexpectedOCIUrlTmpl            = "url not prefixed with oci://, got [%s]"
	devel 				   = ">0.0.0-0"
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
	loginClient     *action.RegistryLogin
}

// ArgsApplier defines helm client arguments helper
type ArgsApplier func(*Args)

// NewClient returns a new Helm Client with provided config
func NewClient(log logging.Logger, restConfig *rest.Config, argAppliers ...ArgsApplier) (Client, error) {

	args := &Args{}
	for _, apply := range argAppliers {
		apply(args)
	}

	rg := newRESTClientGetter(restConfig, args.Namespace)

	actionConfig := new(action.Configuration)
	// Always store helm state in the same cluster/namespace where chart is deployed
	if err := actionConfig.Init(rg, args.Namespace, helmDriverSecret, func(format string, v ...interface{}) {
		log.Debug(fmt.Sprintf(format, v))
	}); err != nil {
		return nil, err
	}

	rc, err := registry.NewClient()
	if err != nil {
		return nil, err
	}
	actionConfig.RegistryClient = rc

	pc := action.NewPullWithOpts(action.WithConfig(actionConfig))

	if _, err := os.Stat(chartCache); os.IsNotExist(err) {
		err = os.Mkdir(chartCache, 0750)
		if err != nil {
			return nil, err
		}
	}

	pc.DestDir = chartCache
	pc.Settings = &cli.EnvSettings{}
	pc.InsecureSkipTLSverify = args.InsecureSkipTLSVerify

	gc := action.NewGet(actionConfig)

	ic := action.NewInstall(actionConfig)
	ic.Namespace = args.Namespace
	ic.Wait = args.Wait
	ic.Timeout = args.Timeout
	ic.SkipCRDs = args.SkipCRDs
	ic.InsecureSkipTLSverify = args.InsecureSkipTLSVerify

	uc := action.NewUpgrade(actionConfig)
	uc.Wait = args.Wait
	uc.Timeout = args.Timeout
	uc.SkipCRDs = args.SkipCRDs
	uc.InsecureSkipTLSverify = args.InsecureSkipTLSVerify

	uic := action.NewUninstall(actionConfig)

	rb := action.NewRollback(actionConfig)
	rb.Wait = args.Wait
	rb.Timeout = args.Timeout

	lc := action.NewRegistryLogin(actionConfig)

	return &client{
		log:             log,
		pullClient:      pc,
		getClient:       gc,
		installClient:   ic,
		upgradeClient:   uc,
		rollbackClient:  rb,
		uninstallClient: uic,
		loginClient:     lc,
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
		return "", err
	}

	chartFileName, err := getChartFileName(tmpDir)
	if err != nil {
		return "", err
	}

	chartFilePath := filepath.Join(chartCache, chartFileName)
	if err := os.Rename(filepath.Join(tmpDir, chartFileName), chartFilePath); err != nil {
		return "", err
	}
	return chartFilePath, nil
}

func (hc *client) pullChart(spec *v1beta1.ChartSpec, creds *RepoCreds, chartDir string) error {
	pc := hc.pullClient

	chartRef := spec.URL
	if spec.URL == "" {
		if registry.IsOCI(spec.Repository) {
			chartRef = resolveOCIChartRef(spec.Repository, spec.Name)
		} else {
			chartRef = spec.Name
			pc.RepoURL = spec.Repository
		}
		pc.Version = spec.Version
	} else if registry.IsOCI(spec.URL) {
		ociURL, version, err := resolveOCIChartVersion(spec.URL)
		if err != nil {
			return err
		}
		pc.Version = version
		chartRef = ociURL.String()
	}
	pc.Username = creds.Username
	pc.Password = creds.Password

	pc.DestDir = chartDir

	err := hc.login(spec, creds, pc.InsecureSkipTLSverify)
	if err != nil {
		return err
	}

	o, err := pc.Run(chartRef)
	hc.log.Debug(o)
	if err != nil {
		return errors.Wrap(err, errFailedToPullChart)
	}
	return nil
}

func (hc *client) login(spec *v1beta1.ChartSpec, creds *RepoCreds, insecure bool) error {
	ociURL := spec.URL
	if spec.URL == "" {
		ociURL = spec.Repository
	}
	if !registry.IsOCI(ociURL) {
		return nil
	}
	parsedURL, err := url.Parse(ociURL)
	if err != nil {
		return errors.Wrap(err, errFailedToParseURL)
	}
	var out strings.Builder
	err = hc.loginClient.Run(&out, parsedURL.Host, creds.Username, creds.Password, insecure)
	hc.log.Debug(out.String())
	return errors.Wrap(err, errFailedToLogin)
}

func (hc *client) PullAndLoadChart(spec *v1beta1.ChartSpec, creds *RepoCreds) (*chart.Chart, error) {
	var chartFilePath string
	var err error
	switch {
	case spec.URL == "" && spec.Version == "" || spec.URL == "" && spec.Version == devel:
		chartFilePath, err = hc.pullLatestChartVersion(spec, creds)
		if err != nil {
			return nil, err
		}
	case registry.IsOCI(spec.URL):
		u, v, err := resolveOCIChartVersion(spec.URL)
		if err != nil {
			return nil, err
		}

		if v == "" {
			chartFilePath, err = hc.pullLatestChartVersion(spec, creds)
			if err != nil {
				return nil, err
			}
		} else {
			chartFilePath = resolveChartFilePath(path.Base(u.Path), v)
		}
	case spec.URL != "":
		u, err := url.Parse(spec.URL)
		if err != nil {
			return nil, errors.Wrap(err, errFailedToParseURL)
		}
		chartFilePath = filepath.Join(chartCache, path.Base(u.Path))
	default:
		chartFilePath = resolveChartFilePath(spec.Name, spec.Version)
	}

	if _, err := os.Stat(chartFilePath); os.IsNotExist(err) {
		if err = hc.pullChart(spec, creds, chartCache); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, errors.Wrap(err, errFailedToCheckIfLocalChartExists)
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

func resolveOCIChartVersion(chartURL string) (*url.URL, string, error) {
	if !registry.IsOCI(chartURL) {
		return nil, "", errors.Errorf(errUnexpectedOCIUrlTmpl, chartURL)
	}
	ociURL, err := url.Parse(chartURL)
	if err != nil {
		return nil, "", errors.Wrap(err, errFailedToParseURL)
	}
	parts := strings.Split(ociURL.Path, ":")
	if len(parts) > 1 {
		ociURL.Path = parts[0]
		return ociURL, parts[1], nil
	}
	return ociURL, "", nil
}

func resolveChartFilePath(name string, version string) string {
	filename := fmt.Sprintf("%s-%s.tgz", name, version)
	return filepath.Join(chartCache, filename)
}

func resolveOCIChartRef(repository string, name string) string {
	return strings.Join([]string{strings.TrimSuffix(repository, "/"), name}, "/")
}

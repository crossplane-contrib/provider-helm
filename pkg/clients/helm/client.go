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
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/registry"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/client-go/rest"
	orasauth "oras.land/oras-go/v2/registry/remote/auth"
	orasretry "oras.land/oras-go/v2/registry/remote/retry"
	ktype "sigs.k8s.io/kustomize/api/types"

	clusterv1beta1 "github.com/crossplane-contrib/provider-helm/apis/cluster/release/v1beta1"
	namespacedv1beta1 "github.com/crossplane-contrib/provider-helm/apis/namespaced/release/v1beta1"
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
	errMissingOCIRegistryHostTmpl      = "missing OCI registry host in url [%s]"
	errFailedToCreateRegistryClient    = "failed to create identity-token registry client"
	errUnexpectedRegistryTransportTmpl = "expected Helm registry transport base to be *http.Transport, got [%T]"
	errFailedToLogin                   = "failed to login to registry"
	errUnexpectedOCIUrlTmpl            = "url not prefixed with oci://, got [%s]"
	devel                              = ">0.0.0-0"
)

// Client is the interface to interact with Helm
type Client interface {
	GetLastRelease(release string) (*release.Release, error)
	Install(release string, chart *chart.Chart, vals map[string]interface{}, patches []ktype.Patch) (*release.Release, error)
	Upgrade(release string, chart *chart.Chart, vals map[string]interface{}, patches []ktype.Patch) (*release.Release, error)
	Rollback(release string) error
	Uninstall(release string) error
	PullAndLoadChart(mg resource.Managed, creds *RepoCreds) (*chart.Chart, error)
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
	// registryClient is the default registry client. pullChart resets to this
	// before and after each pull so an identity-token-scoped client does not leak.
	registryClient *registry.Client
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
	pc.PlainHTTP = args.PlainHTTP

	gc := action.NewGet(actionConfig)

	ic := action.NewInstall(actionConfig)
	ic.Namespace = args.Namespace
	ic.Wait = args.Wait
	ic.Timeout = args.Timeout
	ic.SkipCRDs = args.SkipCRDs
	ic.InsecureSkipTLSverify = args.InsecureSkipTLSVerify
	ic.PlainHTTP = args.PlainHTTP

	uc := action.NewUpgrade(actionConfig)
	uc.Wait = args.Wait
	uc.Timeout = args.Timeout
	uc.SkipCRDs = args.SkipCRDs
	uc.InsecureSkipTLSverify = args.InsecureSkipTLSVerify
	uc.PlainHTTP = args.PlainHTTP

	uic := action.NewUninstall(actionConfig)
	uic.Wait = args.Wait
	uic.Timeout = args.Timeout

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
		registryClient:  rc,
	}, nil
}

func getChartFileName(dir string) (string, error) {
	files, err := os.ReadDir(dir)
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
func (hc *client) pullLatestChartVersion(chartUrl, chartName, chartVersion, chartRepo string, creds *RepoCreds) (string, error) {
	tmpDir, err := os.MkdirTemp(chartCache, "")
	if err != nil {
		return "", err
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			hc.log.WithValues("tmpDir", tmpDir).Info("failed to remove temporary directory")
		}
	}()

	if err := hc.pullChart(chartUrl, chartName, chartVersion, chartRepo, creds, tmpDir); err != nil {
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

func (hc *client) pullChart(chartUrl, chartName, chartVersion, chartRepo string, creds *RepoCreds, chartDir string) error {
	pc := hc.pullClient
	resetPullState(pc)

	chartRef := chartUrl
	if chartUrl == "" {
		if registry.IsOCI(chartRepo) {
			chartRef = resolveOCIChartRef(chartRepo, chartName)
		} else {
			chartRef = chartName
			pc.RepoURL = chartRepo
		}
		pc.Version = chartVersion
	} else if registry.IsOCI(chartUrl) {
		ociURL, version, err := resolveOCIChartVersion(chartUrl)
		if err != nil {
			return err
		}
		pc.Version = version
		chartRef = ociURL.String()
	}
	configurePullCredentials(pc, chartUrl, chartRepo, creds)

	pc.DestDir = chartDir

	defer hc.pullClient.SetRegistryClient(hc.registryClient)
	if err := hc.configureRegistryClient(chartUrl, chartRepo, creds); err != nil {
		return err
	}

	if creds.hasBasicAuth() && !usesIdentityToken(chartUrl, chartRepo, creds) {
		err := hc.login(chartUrl, chartRepo, creds, pc.InsecureSkipTLSverify)
		if err != nil {
			return err
		}
	}

	o, err := pc.Run(chartRef)
	hc.log.Debug(o)
	if err != nil {
		return errors.Wrap(err, errFailedToPullChart)
	}
	return nil
}

func (hc *client) configureRegistryClient(chartUrl, chartRepo string, creds *RepoCreds) error {
	hc.pullClient.SetRegistryClient(hc.registryClient)

	if !usesIdentityToken(chartUrl, chartRepo, creds) {
		return nil
	}

	parsedURL, err := url.Parse(ociURL(chartUrl, chartRepo))
	if err != nil {
		return errors.Wrap(err, errFailedToParseURL)
	}
	if parsedURL.Host == "" {
		return errors.Errorf(errMissingOCIRegistryHostTmpl, ociURL(chartUrl, chartRepo))
	}

	rc, err := identityTokenRegistryClient(parsedURL.Host, creds, hc.pullClient.InsecureSkipTLSverify, hc.pullClient.PlainHTTP)
	if err != nil {
		return err
	}
	hc.pullClient.SetRegistryClient(rc)
	return nil
}

func usesIdentityToken(chartUrl, chartRepo string, creds *RepoCreds) bool {
	return creds.hasIdentityToken() && registry.IsOCI(ociURL(chartUrl, chartRepo))
}

func resetPullState(pc *action.Pull) {
	pc.Username = ""
	pc.Password = ""
	pc.Version = ""
	pc.RepoURL = ""
}

func configurePullCredentials(pc *action.Pull, chartUrl, chartRepo string, creds *RepoCreds) {
	pc.Username = ""
	pc.Password = ""
	if creds.hasBasicAuth() && !usesIdentityToken(chartUrl, chartRepo, creds) {
		pc.Username = creds.Username
		pc.Password = creds.Password
	}
}

func ociURL(chartUrl, chartRepo string) string {
	if chartUrl != "" {
		return chartUrl
	}
	return chartRepo
}

func identityTokenRegistryClient(host string, creds *RepoCreds, insecureSkipTLSVerify, plainHTTP bool) (*registry.Client, error) {
	httpClient, err := registryHTTPClient(insecureSkipTLSVerify)
	if err != nil {
		return nil, errors.Wrap(err, errFailedToCreateRegistryClient)
	}

	authorizer := orasauth.Client{
		Client:     httpClient,
		Credential: orasauth.StaticCredential(host, creds.registryCredential()),
	}

	opts := []registry.ClientOption{
		registry.ClientOptHTTPClient(httpClient),
		registry.ClientOptAuthorizer(authorizer),
	}
	if plainHTTP {
		opts = append(opts, registry.ClientOptPlainHTTP())
	}

	rc, err := registry.NewClient(opts...)
	return rc, errors.Wrap(err, errFailedToCreateRegistryClient)
}

func registryHTTPClient(insecureSkipTLSVerify bool) (*http.Client, error) {
	// Keep Helm's normal retry transport. The flag disables Helm registry
	// debug logging; it is unrelated to TLS verification.
	return registryHTTPClientForTransport(registry.NewTransport(false), insecureSkipTLSVerify)
}

func registryHTTPClientForTransport(transport *orasretry.Transport, insecureSkipTLSVerify bool) (*http.Client, error) {
	if !insecureSkipTLSVerify {
		return &http.Client{Transport: transport}, nil
	}

	base, ok := transport.Base.(*http.Transport)
	if !ok {
		return nil, errors.Errorf(errUnexpectedRegistryTransportTmpl, transport.Base)
	}

	base = base.Clone()
	if base.TLSClientConfig == nil {
		base.TLSClientConfig = &tls.Config{} //nolint:gosec // This honors the Release's explicit insecureSkipTLSVerify setting.
	} else {
		base.TLSClientConfig = base.TLSClientConfig.Clone()
	}
	base.TLSClientConfig.InsecureSkipVerify = true //nolint:gosec // This honors the Release's explicit insecureSkipTLSVerify setting.
	transport.Base = base

	return &http.Client{Transport: transport}, nil
}

func (hc *client) login(chartUrl, chartRepo string, creds *RepoCreds, insecure bool) error {
	registryURL := ociURL(chartUrl, chartRepo)
	if !registry.IsOCI(registryURL) {
		return nil
	}
	parsedURL, err := url.Parse(registryURL)
	if err != nil {
		return errors.Wrap(err, errFailedToParseURL)
	}
	var out strings.Builder
	err = hc.loginClient.Run(&out, parsedURL.Host, creds.Username, creds.Password, action.WithInsecure(insecure))
	hc.log.Debug(out.String())
	return errors.Wrap(err, errFailedToLogin)
}

func (hc *client) PullAndLoadChart(mg resource.Managed, creds *RepoCreds) (*chart.Chart, error) { //nolint:gocyclo
	var chartFilePath, chartUrl, chartName, chartVersion, chartRepo string
	var err error

	switch r := mg.(type) {
	case *clusterv1beta1.Release:
		chartUrl = r.Spec.ForProvider.Chart.URL
		chartVersion = r.Spec.ForProvider.Chart.Version
		chartName = r.Spec.ForProvider.Chart.Name
		chartRepo = r.Spec.ForProvider.Chart.Repository
	case *namespacedv1beta1.Release:
		chartUrl = r.Spec.ForProvider.Chart.URL
		chartVersion = r.Spec.ForProvider.Chart.Version
		chartName = r.Spec.ForProvider.Chart.Name
		chartRepo = r.Spec.ForProvider.Chart.Repository
	default:
		return nil, errors.New("This object must be *clusterv1beta1.Release or *namespacedv1beta1.Release")
	}

	switch {
	case chartUrl == "" && (chartVersion == "" || chartVersion == devel):
		chartFilePath, err = hc.pullLatestChartVersion(chartUrl, chartName, chartVersion, chartRepo, creds)
		if err != nil {
			return nil, err
		}
	case registry.IsOCI(chartUrl):
		u, v, err := resolveOCIChartVersion(chartUrl)
		if err != nil {
			return nil, err
		}

		if v == "" {
			chartFilePath, err = hc.pullLatestChartVersion(chartUrl, chartName, chartVersion, chartRepo, creds)
			if err != nil {
				return nil, err
			}
		} else {
			chartFilePath = resolveChartFilePath(path.Base(u.Path), v)
		}
	case chartUrl != "":
		u, err := url.Parse(chartUrl)
		if err != nil {
			return nil, errors.Wrap(err, errFailedToParseURL)
		}
		chartFilePath = filepath.Join(chartCache, path.Base(u.Path))
	default:
		chartFilePath = resolveChartFilePath(chartName, chartVersion)
	}

	if _, err := os.Stat(chartFilePath); os.IsNotExist(err) {
		if err = hc.pullChart(chartUrl, chartName, chartVersion, chartRepo, creds, chartCache); err != nil {
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

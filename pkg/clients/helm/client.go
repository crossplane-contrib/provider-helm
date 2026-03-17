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
	ktype "sigs.k8s.io/kustomize/api/types"

	clusterv1beta1 "github.com/crossplane-contrib/provider-helm/apis/cluster/release/v1beta1"
	namespacedv1beta1 "github.com/crossplane-contrib/provider-helm/apis/namespaced/release/v1beta1"
)

const (
	helmDriverSecret = "secret"
	// releaseMaxHistory is the maximum number of entries Helm will keep in
	// release history. We just set a reasonable default for our use case.
	releaseMaxHistory = 20
)

// chartCache is the directory where pulled chart tarballs are stored. It is
// mutable in tests so that they can override it with a temporary location.
var chartCache = "/tmp/charts"

const (
	errFailedToCheckIfLocalChartExists = "failed to check if cached chart file exists"
	errFailedToPullChart               = "failed to pull chart"
	errFailedToLoadChart               = "failed to load chart"
	errUnexpectedDirContentTmpl        = "expected 1 .tgz chart file, got [%s]"
	errFailedToParseURL                = "failed to parse URL"
	errFailedToLogin                   = "failed to login to registry"
	errUnexpectedOCIUrlTmpl            = "url not prefixed with oci://, got [%s]"
	errDigestNotSupportedForNonOCI     = "digest is only supported for OCI registries"
	errDigestMismatchTmpl              = "conflicting digest input: URL contains @%s but spec.forProvider.chart.digest is %s"
	errNoChartName                     = "spec.forProvider.chart.name must be specified when URL is empty"
	errNoChartRepository               = "spec.forProvider.chart.repository must be specified when URL is empty"
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
	ic.TakeOwnership = args.TakeOwnership

	uc := action.NewUpgrade(actionConfig)
	uc.Wait = args.Wait
	uc.Timeout = args.Timeout
	uc.SkipCRDs = args.SkipCRDs
	uc.InsecureSkipTLSverify = args.InsecureSkipTLSVerify
	uc.PlainHTTP = args.PlainHTTP
	uc.TakeOwnership = args.TakeOwnership

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
	}, nil
}

// safePath constructs a safe file path by sanitizing the filename component
// to prevent path traversal attacks. It ensures only the base filename is used.
func safePath(baseDir, fileName string) string {
	return filepath.Join(baseDir, filepath.Base(fileName))
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

// pullChartToCache pulls a chart into the cache and returns its absolute path.
func (hc *client) pullChartToCache(chartUrl, chartName, chartVersion, chartRepo, chartDigest string, creds *RepoCreds) (string, error) {
	tmpDir, err := os.MkdirTemp(chartCache, "")
	if err != nil {
		return "", err
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			hc.log.WithValues("tmpDir", tmpDir).Info("failed to remove temporary directory")
		}
	}()

	if err := hc.pullChart(chartUrl, chartName, chartVersion, chartRepo, chartDigest, creds, tmpDir); err != nil {
		return "", err
	}

	pulledName, err := getChartFileName(tmpDir)
	if err != nil {
		return "", err
	}
	chartFilePath := filepath.Join(chartCache, pulledName)
	if err := os.Rename(filepath.Join(tmpDir, pulledName), chartFilePath); err != nil {
		return "", err
	}
	return chartFilePath, nil
}

func (hc *client) pullChart(chartUrl, chartName, chartVersion, chartRepo, chartDigest string, creds *RepoCreds, chartDir string) error {
	pc := hc.pullClient

	chartRef := chartUrl
	if chartUrl == "" {
		if registry.IsOCI(chartRepo) {
			chartRef = resolveOCIChartRef(chartRepo, chartName, chartDigest)
		} else {
			chartRef = chartName
			pc.RepoURL = chartRepo
		}
		pc.Version = chartVersion
	} else if registry.IsOCI(chartUrl) {
		ociURL, version, urlDigest, err := resolveOCIChartVersionAndDigest(chartUrl)
		if err != nil {
			return err
		}
		pc.Version = version
		chartRef = ociURL.String()

		effectiveDigest, err := resolveEffectiveDigest(urlDigest, chartDigest)
		if err != nil {
			return err
		}
		// Append digest if present (per Helm PR #12690)
		if effectiveDigest != "" {
			chartRef = chartRef + "@" + effectiveDigest
		}
	}
	pc.Username = creds.Username
	pc.Password = creds.Password

	pc.DestDir = chartDir

	if creds.Username != "" && creds.Password != "" {
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

func (hc *client) login(chartUrl, chartRepo string, creds *RepoCreds, insecure bool) error {
	ociURL := chartUrl
	if chartUrl == "" {
		ociURL = chartRepo
	}
	if !registry.IsOCI(ociURL) {
		return nil
	}
	parsedURL, err := url.Parse(ociURL)
	if err != nil {
		return errors.Wrap(err, errFailedToParseURL)
	}
	var out strings.Builder
	err = hc.loginClient.Run(&out, parsedURL.Host, creds.Username, creds.Password, action.WithInsecure(insecure))
	hc.log.Debug(out.String())
	return errors.Wrap(err, errFailedToLogin)
}

// ensureChartCached verifies a chart exists in the cache and pulls it if
// missing. chartFilePath is sanitized with filepath.Base before use, so
// directory components in the input cannot escape chartCache. Returns the
// final absolute path to the cached chart file or an error.
func (hc *client) ensureChartCached(chartFilePath, chartUrl, chartName, chartVersion, chartRepo, chartDigest string, creds *RepoCreds) (string, error) {
	if chartFilePath == "" {
		hc.log.Debug("no cache path for chart", "URL", chartUrl, "name", chartName, "version", chartVersion, "repo", chartRepo, "digest", chartDigest)
		return hc.pullChartToCache(chartUrl, chartName, chartVersion, chartRepo, chartDigest, creds)
	}
	cachedPath := filepath.Join(chartCache, filepath.Base(chartFilePath))
	fileInfo, err := os.Stat(cachedPath)
	switch {
	case os.IsNotExist(err):
		hc.log.Debug("cache miss for chart", "cachedPath", cachedPath, "URL", chartUrl, "name", chartName, "version", chartVersion, "repo", chartRepo, "digest", chartDigest)
		return hc.pullChartToCache(chartUrl, chartName, chartVersion, chartRepo, chartDigest, creds)
	case err != nil:
		return "", errors.Wrap(err, errFailedToCheckIfLocalChartExists)
	case fileInfo.IsDir():
		return "", errors.New("expected chart file, got directory")
	}

	hc.log.Debug("cache hit for chart", "cachedPath", cachedPath, "URL", chartUrl, "name", chartName, "version", chartVersion, "repo", chartRepo, "digest", chartDigest)
	return cachedPath, nil
}

func resolveEffectiveDigest(urlDigest, specDigest string) (string, error) {
	if specDigest != "" && urlDigest != "" && urlDigest != specDigest {
		return "", errors.Errorf(errDigestMismatchTmpl, urlDigest, specDigest)
	}
	if urlDigest != "" {
		return urlDigest, nil
	}
	return specDigest, nil
}

func (hc *client) PullAndLoadChart(mg resource.Managed, creds *RepoCreds) (*chart.Chart, error) { //nolint:gocyclo
	var chartFilePath, chartUrl, chartName, chartVersion, chartDigest, chartRepo string
	var err error

	switch r := mg.(type) {
	case *clusterv1beta1.Release:
		chartUrl = r.Spec.ForProvider.Chart.URL
		chartVersion = r.Spec.ForProvider.Chart.Version
		chartName = r.Spec.ForProvider.Chart.Name
		chartRepo = r.Spec.ForProvider.Chart.Repository
		chartDigest = r.Spec.ForProvider.Chart.Digest
	case *namespacedv1beta1.Release:
		chartUrl = r.Spec.ForProvider.Chart.URL
		chartVersion = r.Spec.ForProvider.Chart.Version
		chartName = r.Spec.ForProvider.Chart.Name
		chartRepo = r.Spec.ForProvider.Chart.Repository
		chartDigest = r.Spec.ForProvider.Chart.Digest
	default:
		return nil, errors.New("This object must be *clusterv1beta1.Release or *namespacedv1beta1.Release")
	}

	// Validate: Digest only works with OCI registries
	if chartDigest != "" {
		isOCI := registry.IsOCI(chartUrl) || registry.IsOCI(chartRepo)
		if !isOCI {
			return nil, errors.New(errDigestNotSupportedForNonOCI)
		}
	}

	switch {
	case chartUrl == "" && (chartVersion == "" || chartVersion == devel) && chartDigest == "":
		// No URL, no version, no digest -> pull latest
		chartFilePath, err = hc.pullChartToCache(chartUrl, chartName, chartVersion, chartRepo, chartDigest, creds)
		if err != nil {
			return nil, err
		}
	case registry.IsOCI(chartUrl):
		u, v, urlDigest, err := resolveOCIChartVersionAndDigest(chartUrl)
		if err != nil {
			return nil, err
		}

		// validate
		effectiveDigest, err := resolveEffectiveDigest(urlDigest, chartDigest)
		if err != nil {
			return nil, err
		}

		switch {
		case effectiveDigest != "":
			// Validate cached chart against the effective digest, and store any
			// pull under the same digest-keyed name.
			name := path.Base(u.Path)
			chartFilePath = resolveCachedChartPathWithDigest(name, effectiveDigest)
		case v == "":
			// No version or digest in URL: pull latest
			chartFilePath, err = hc.pullChartToCache(chartUrl, chartName, chartVersion, chartRepo, chartDigest, creds)
			if err != nil {
				return nil, err
			}
		default:
			chartFilePath = resolveChartFilePath(path.Base(u.Path), v)
		}
	case chartUrl != "":
		// Non-OCI URL(e.g. HTTP/HTTPS)
		u, err := url.Parse(chartUrl)
		if err != nil {
			return nil, errors.Wrap(err, errFailedToParseURL)
		}
		chartFilePath = filepath.Join(chartCache, path.Base(u.Path))
	default:
		// No URL: resolve from spec Repository + Name + Version + (optionally Digest)
		switch {
		case chartName == "":
			return nil, errors.New(errNoChartName)
		case chartRepo == "":
			return nil, errors.New(errNoChartRepository)
		case chartDigest != "":
			chartFilePath = resolveCachedChartPathWithDigest(chartName, chartDigest)
		default:
			chartFilePath = resolveChartFilePath(chartName, chartVersion)
		}
	}

	chartFilePath, err = hc.ensureChartCached(chartFilePath, chartUrl, chartName, chartVersion, chartRepo, chartDigest, creds)
	if err != nil {
		return nil, err
	}

	// Load chart from cache using safe path construction
	realPath := safePath(chartCache, chartFilePath)
	chart, err := loader.Load(realPath)
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

// resolveOCIChartVersionAndDigest extracts version and digest from OCI chart URL.
// Supports: oci://registry/chart, oci://registry/chart:version,
//
//	oci://registry/chart@sha256:..., oci://registry/chart:version@sha256:...
//
// Returns: (baseURL, version, digest, error)
func resolveOCIChartVersionAndDigest(chartURL string) (*url.URL, string, string, error) {
	if !registry.IsOCI(chartURL) {
		return nil, "", "", errors.Errorf(errUnexpectedOCIUrlTmpl, chartURL)
	}
	ociURL, err := url.Parse(chartURL)
	if err != nil {
		return nil, "", "", errors.Wrap(err, errFailedToParseURL)
	}

	path := ociURL.Path
	version := ""
	digest := ""

	// Extract digest first (after @)
	if atIndex := strings.LastIndex(path, "@"); atIndex != -1 {
		digest = path[atIndex+1:]
		path = path[:atIndex]
	}

	// Extract version (after :)
	if colonIndex := strings.LastIndex(path, ":"); colonIndex != -1 {
		version = path[colonIndex+1:]
		path = path[:colonIndex]
	}

	ociURL.Path = path
	return ociURL, version, digest, nil
}

func resolveOCIChartVersion(chartURL string) (*url.URL, string, error) {
	u, v, _, err := resolveOCIChartVersionAndDigest(chartURL)
	return u, v, err
}

// resolveChartFilePath returns the expected location of a chart tarball in the
// cache given the chart name and version. It is equivalent to
// filepath.Join(base, fmt.Sprintf("%s-%s.tgz", name, version)) where base is
// the directory used by the client for its cache.
func resolveChartFilePath(name, version string) string {
	return resolveChartFilePathWithBase(chartCache, name, version)
}

// resolveChartFilePathWithBase is a helper that mirrors resolveChartFilePath but
// allows callers (most importantly tests) to supply an arbitrary base directory
// instead of the package-wide chartCache variable.
func resolveChartFilePathWithBase(base, name, version string) string {
	filename := fmt.Sprintf("%s-%s.tgz", name, version)
	return filepath.Join(base, filename)
}

func resolveOCIChartRef(repository, name, digest string) string {
	ref := strings.Join([]string{strings.TrimSuffix(repository, "/"), name}, "/")
	if d := strings.TrimSpace(digest); d != "" {
		ref += "@" + d
	}
	return ref
}

func resolveCachedChartPathWithDigest(chartName, digest string) string {
	// Cannot construct cache path without name
	if chartName == "" {
		return ""
	}
	algo, hashSum, found := strings.Cut(digest, ":")
	if !found {
		return ""
	}
	filename := fmt.Sprintf("%s@%s-%s.tgz", filepath.Base(chartName), algo, hashSum)
	return filepath.Join(chartCache, filename)
}

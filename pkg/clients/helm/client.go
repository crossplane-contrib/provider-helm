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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
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
	errDigestNotSupportedForNonOCI     = "digest is only supported for OCI registries"
	errDigestMismatchTmpl              = "digest mismatch: URL contains @%s but spec.forProvider.chart.digest is %s"
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
		ociURL, version, digest, err := resolveOCIChartVersionAndDigest(chartUrl)
		if err != nil {
			return err
		}
		pc.Version = version
		chartRef = ociURL.String()

		// Append digest if present (per Helm PR #12690)
		if digest != "" {
			chartRef = chartRef + "@" + digest
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
		chartFilePath, err = hc.pullLatestChartVersion(chartUrl, chartName, chartVersion, chartRepo, creds)
		if err != nil {
			return nil, err
		}
	case registry.IsOCI(chartUrl):
		// OCI URL provided
		u, v, d, err := resolveOCIChartVersionAndDigest(chartUrl)
		if err != nil {
			return nil, err
		}

		switch {
		case d != "":
			// Digest in URL: check if cached version matches digest
			if v != "" {
				cachedPath := resolveChartFilePath(path.Base(u.Path), v)
				if match, cachedDigest, err := cacheDigestMatches(cachedPath, d); err != nil {
					// can't read cached file: treat as miss
					hc.log.Debug("failed to verify cached digest, will pull fresh", "path", cachedPath, "err", err)
					chartFilePath = ""
				} else if match {
					hc.log.Debug("Cache hit: digest matches", "path", cachedPath, "digest", d)
					chartFilePath = cachedPath
				} else {
					hc.log.Debug("Cache digest mismatch, will pull fresh", "path", cachedPath, "requestedDigest", d, "cachedDigest", cachedDigest)
					chartFilePath = ""
				}
			} else {
				// No version to construct cache path, must pull
				chartFilePath = ""
			}
		case v == "":
			// No version in URL: pull latest
			chartFilePath, err = hc.pullLatestChartVersion(chartUrl, chartName, chartVersion, chartRepo, creds)
			if err != nil {
				return nil, err
			}
		default:
			// Version in URL: use cache path
			chartFilePath = resolveChartFilePath(path.Base(u.Path), v)
		}
	case chartUrl != "":
		// HTTP/HTTPS URL
		u, err := url.Parse(chartUrl)
		if err != nil {
			return nil, errors.Wrap(err, errFailedToParseURL)
		}
		chartFilePath = filepath.Join(chartCache, path.Base(u.Path))
	default:
		// Repository + Name + Version + (optionally Digest)
		if chartDigest != "" {
			// Check if cached version matches digest
			if chartName != "" && chartVersion != "" {
				cachedPath := resolveChartFilePath(chartName, chartVersion)
				if match, cachedDigest, err := cacheDigestMatches(cachedPath, chartDigest); err != nil {
					hc.log.Debug("failed to verify cached digest, will pull fresh", "path", cachedPath, "err", err)
					chartFilePath = ""
				} else if match {
					hc.log.Debug("Cache hit: digest matches", "path", cachedPath, "digest", chartDigest)
					chartFilePath = cachedPath
				} else {
					hc.log.Debug("Cache digest mismatch, will pull fresh", "path", cachedPath, "requestedDigest", chartDigest, "cachedDigest", cachedDigest)
					chartFilePath = ""
				}
			} else {
				// No name/version to construct cache path, must pull
				chartFilePath = ""
			}
		} else {
			chartFilePath = resolveChartFilePath(chartName, chartVersion)
		}
	}

	// Handle cache misses (digest verification already done above)
	if chartFilePath == "" {
		pullUrl := chartUrl
		if chartUrl == "" && chartDigest != "" {
			// Build OCI URL from repository + name
			pullUrl = resolveOCIChartRef(chartRepo, chartName)
			if chartVersion != "" {
				pullUrl = pullUrl + ":" + chartVersion
			}
			pullUrl = pullUrl + "@" + chartDigest
		} else if chartUrl != "" && chartDigest != "" {
			// Check if URL already contains a digest
			if strings.Contains(chartUrl, "@") {
				// URL already has a digest - verify it matches
				_, _, urlDigest, err := resolveOCIChartVersionAndDigest(chartUrl)
				if err != nil {
					return nil, err
				}
				if urlDigest != "" && urlDigest != chartDigest {
					// Conflicting digests between URL and field
					return nil, errors.Errorf(errDigestMismatchTmpl, urlDigest, chartDigest)
				}
				// Digests match or URL digest is empty, use URL as-is
				pullUrl = chartUrl
			} else {
				// No digest in URL, append the one from spec
				pullUrl = chartUrl + "@" + chartDigest
			}
		}

		chartFilePath, err = hc.pullLatestChartVersion(pullUrl, chartName, chartVersion, chartRepo, creds)
		if err != nil {
			return nil, err
		}
	} else {
		// Open root directory for secure file access (prevents path traversal at kernel level)
		root, err := os.OpenRoot(chartCache)
		if err != nil {
			return nil, errors.Wrap(err, "failed to open chart cache directory")
		}
		defer func() {
			if cerr := root.Close(); cerr != nil {
				hc.log.WithValues("chartCache", chartCache, "error", cerr).Info("failed to close root directory")
			}
		}()

		// filepath.Base() strips directory components, neutralizing path traversal in chartFilePath
		chartFileName := filepath.Base(chartFilePath)

		// Check if chart exists in cache
		// os.Root provides kernel-level protection against path traversal
		fileInfo, err := root.Stat(chartFileName)
		switch {
		case os.IsNotExist(err):
			// Normal cache miss: pull chart into temp dir then atomically move into cache
			tmpDir, err := os.MkdirTemp(chartCache, "")
			if err != nil {
				return nil, err
			}
			defer func() {
				if err := os.RemoveAll(tmpDir); err != nil {
					hc.log.WithValues("tmpDir", tmpDir).Info("failed to remove temporary directory")
				}
			}()

			if err = hc.pullChart(chartUrl, chartName, chartVersion, chartRepo, creds, tmpDir); err != nil {
				return nil, err
			}

			// Determine the pulled file name
			pulledName, err := getChartFileName(tmpDir)
			if err != nil {
				return nil, err
			}

			// Atomically move pulled artifact into cache using safe path construction
			dstPath := safePath(chartCache, chartFileName)
			srcPath := safePath(tmpDir, pulledName)
			// G703: safePath() sanitizes with filepath.Base() preventing traversal
			if err := os.Rename(srcPath, dstPath); err != nil { //nolint:gosec
				return nil, err
			}
		case err != nil:
			return nil, errors.Wrap(err, errFailedToCheckIfLocalChartExists)
		case fileInfo.IsDir():
			return nil, errors.New("expected chart file, got directory")
		}

		// Update chartFilePath for final load
		chartFilePath = filepath.Join(chartCache, chartFileName)
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

func resolveChartFilePath(name string, version string) string {
	filename := fmt.Sprintf("%s-%s.tgz", name, version)
	return filepath.Join(chartCache, filename)
}

func resolveOCIChartRef(repository string, name string) string {
	return strings.Join([]string{strings.TrimSuffix(repository, "/"), name}, "/")
}

// computeFileDigest computes the SHA256 digest of a chart file.
// The digest string is returned in the OCI format "sha256:<hex>".
// The filePath is sanitized to only use the base filename, preventing path traversal.
func computeFileDigest(filePath string) (string, error) {
	// Determine the base directory from the input path
	// In production: chartCache (/tmp/charts)
	// In tests: the directory containing the file
	baseDir := filepath.Dir(filePath)
	if baseDir == "." || baseDir == "" {
		baseDir = chartCache
	}

	// Use safePath to sanitize and construct the file path
	securePath := safePath(baseDir, filePath)
	// G304: safePath() sanitizes with filepath.Base() preventing traversal
	f, err := os.Open(securePath) //nolint:gosec
	if err != nil {
		return "", err
	}
	defer func() {
		_ = f.Close()
	}()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// cacheDigestMatches computes the digest of the chart at cachedPath and
// compares it to the expected value. It returns a boolean indicating whether
// they match, the computed digest (empty if an error occurred), and any error
// encountered while reading the file. The caller may log the error or take
// other action; a non-match with a nil error simply means the contents are
// different.
func cacheDigestMatches(cachedPath, expected string) (bool, string, error) {
	digest, err := computeFileDigest(cachedPath)
	if err != nil {
		return false, "", err
	}
	return digest == expected, digest, nil
}

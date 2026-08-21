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
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"helm.sh/helm/v4/pkg/action"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/registry"
	release "helm.sh/helm/v4/pkg/release/v1"
	"k8s.io/client-go/rest"
	ktype "sigs.k8s.io/kustomize/api/types"

	clusterv1beta1 "github.com/crossplane-contrib/provider-helm/apis/cluster/release/v1beta1"
	namespacedv1beta1 "github.com/crossplane-contrib/provider-helm/apis/namespaced/release/v1beta1"
)

const (
	helmDriverSecret  = "secret"
	chartContentCache = "/tmp/content-cache"
)

// chartCache is the directory where pulled chart tarballs are stored. It is
// mutable in tests so that they can override it with a temporary location.
var chartCache = "/tmp/charts"

// chtimesFn is os.Chtimes, indirected so tests can simulate the file having
// vanished between writeCABundleToFile's Stat and this call (e.g. a
// concurrent sweep) without needing a genuine data race to hit that branch.
var chtimesFn = os.Chtimes

// caBundleCacheDir is where content-addressed CA bundle files are written,
// each keyed by the SHA-256 of its content. Unlike chartCache, which is
// name-keyed and reused verbatim across reconciles, this exists specifically
// so that repeated calls with the same content reuse the same file instead
// of accumulating a new one every time. Mutable in tests.
var caBundleCacheDir = "/tmp/ca-bundles"

// caBundleTTL is how long an unused CA bundle file is kept before an
// opportunistic sweep removes it. It is far longer than any realistic
// reconcile duration, so there's no correctness dependency on this value -
// only a bound on how much a long-lived pod accumulates under /tmp. Mutable
// in tests so they don't have to wait a day.
var caBundleTTL = 24 * time.Hour

// systemCACandidates mirrors the well-known Linux CA bundle paths crypto/x509's
// own root_linux.go checks, since there's no public API to read back the raw
// PEM bytes x509.SystemCertPool() used internally - only these candidate
// paths are known to potentially exist. Checked in order, first readable one
// wins. Mutable in tests.
var systemCACandidates = []string{
	"/etc/ssl/certs/ca-certificates.crt",                // Debian/Ubuntu/Gentoo etc.
	"/etc/pki/tls/certs/ca-bundle.crt",                  // Fedora/RHEL
	"/etc/ssl/ca-bundle.pem",                            // OpenSUSE
	"/etc/pki/tls/cacert.pem",                           // OpenELEC
	"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem", // CentOS/RHEL 7
	"/etc/ssl/cert.pem",                                 // Alpine Linux
}

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
	errFailedToInitActionConfig        = "failed to initialize helm action configuration"
	errFailedToCreateRegistryClient    = "failed to create registry client"
	errFailedToCreateChartCacheDir     = "failed to create chart cache directory"
	errFailedToCreateContentCacheDir   = "failed to create chart content cache directory"
	errFailedToParseCABundle           = "failed to parse CA bundle: no PEM certificates found"
	errFailedToWriteCABundle           = "failed to write CA bundle to a temporary file"
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
	// Helm v4 discards its internal logs (including kstatus wait diagnostics)
	// unless a handler is set, and Init copies the handler into the kube
	// client and storage driver, so this must run before Init.
	actionConfig.SetLogger(slogHandler{log: log})
	// Always store helm state in the same cluster/namespace where chart is deployed
	if err := actionConfig.Init(rg, args.Namespace, helmDriverSecret); err != nil {
		return nil, errors.Wrap(err, errFailedToInitActionConfig)
	}

	// caFile is only set when a CABundle is supplied. It configures the
	// classic HTTP(S) chart-repository getter (used by Pull/Install/Upgrade
	// for non-OCI repos). The OCI registry path below does not read this
	// field at all - it goes through its own *registry.Client - so both
	// need to be configured for a CABundle to cover both protocols.
	var caFile string
	registryOpts := []registry.ClientOption{}
	if len(args.CABundle) > 0 {
		httpClient, err := httpClientTrustingCABundle(log, args.CABundle)
		if err != nil {
			return nil, err
		}
		registryOpts = append(registryOpts, registry.ClientOptHTTPClient(httpClient))

		// Unlike the OCI path above (which builds its cert pool from
		// x509.SystemCertPool(), always including the system trust store),
		// helm's classic getter replaces its cert pool entirely with
		// whatever CaFile contains - so writing just args.CABundle here
		// would make classic repos trust ONLY the supplied bundle, while
		// OCI trusts system roots plus the bundle. Concatenate the system
		// bundle in (best-effort - there's no public API to read back the
		// raw PEM bytes x509.SystemCertPool() used, so this reads a known
		// file path directly) to keep both paths' trust semantics the same.
		classicBundle := args.CABundle
		if systemBundle, systemPath := readSystemCABundle(); len(systemBundle) > 0 {
			classicBundle = make([]byte, 0, len(systemBundle)+1+len(args.CABundle))
			classicBundle = append(classicBundle, systemBundle...)
			classicBundle = append(classicBundle, '\n')
			classicBundle = append(classicBundle, args.CABundle...)
			log.Debug("including system CA bundle alongside the supplied CABundle for classic chart-repo TLS", "path", systemPath)
		} else {
			log.Info("no readable system CA bundle found; classic chart-repo TLS will trust only the supplied CABundle, not the system trust store (OCI registries are unaffected, they use crypto/x509.SystemCertPool directly)")
		}

		caFile, err = writeCABundleToFile(classicBundle)
		if err != nil {
			return nil, errors.Wrap(err, errFailedToWriteCABundle)
		}
	}

	rc, err := registry.NewClient(registryOpts...)
	if err != nil {
		return nil, errors.Wrap(err, errFailedToCreateRegistryClient)
	}
	actionConfig.RegistryClient = rc

	pc := action.NewPull(action.WithConfig(actionConfig))

	if _, err := os.Stat(chartCache); os.IsNotExist(err) {
		err = os.Mkdir(chartCache, 0750)
		if err != nil {
			return nil, errors.Wrap(err, errFailedToCreateChartCacheDir)
		}
	}

	pc.DestDir = chartCache
	// Helm v4's downloader requires a content cache path; an empty
	// EnvSettings causes "content cache must be set" from chart pull.
	if _, err := os.Stat(chartContentCache); os.IsNotExist(err) {
		err = os.Mkdir(chartContentCache, 0750)
		if err != nil {
			return nil, errors.Wrap(err, errFailedToCreateContentCacheDir)
		}
	}

	pc.Settings = &cli.EnvSettings{
		ContentCache: chartContentCache,
	}
	pc.InsecureSkipTLSVerify = args.InsecureSkipTLSVerify
	pc.PlainHTTP = args.PlainHTTP
	pc.CaFile = caFile

	gc := action.NewGet(actionConfig)

	// Helm v4 replaced the boolean wait with wait strategies. This mapping
	// follows helm's own shim for the deprecated --wait flag (pkg/cmd/flags.go):
	// wait=false still waits for hook Pods/Jobs only, matching v3, while
	// wait=true now waits on kstatus readiness instead of v3's poller — a
	// deliberate semantic change: the v3-compatible kube.LegacyStrategy is not
	// used because the poller has false positives, e.g. it reports ready with
	// zero ready pods when a Deployment's replicas - maxUnavailable == 0.
	waitStrategy := kube.HookOnlyStrategy
	if args.Wait {
		waitStrategy = kube.StatusWatcherStrategy
	}

	ic := action.NewInstall(actionConfig)
	ic.Namespace = args.Namespace
	ic.WaitStrategy = waitStrategy
	ic.Timeout = args.Timeout
	ic.SkipCRDs = args.SkipCRDs
	ic.InsecureSkipTLSVerify = args.InsecureSkipTLSVerify
	ic.PlainHTTP = args.PlainHTTP
	ic.CaFile = caFile
	ic.TakeOwnership = args.TakeOwnership
	ic.ForceConflicts = args.SSAForceConflicts

	uc := action.NewUpgrade(actionConfig)
	uc.WaitStrategy = waitStrategy
	uc.Timeout = args.Timeout
	uc.SkipCRDs = args.SkipCRDs
	uc.InsecureSkipTLSVerify = args.InsecureSkipTLSVerify
	uc.PlainHTTP = args.PlainHTTP
	uc.CaFile = caFile
	uc.TakeOwnership = args.TakeOwnership
	uc.MaxHistory = args.MaxHistory
	uc.ForceConflicts = args.SSAForceConflicts

	uic := action.NewUninstall(actionConfig)
	uic.WaitStrategy = waitStrategy
	uic.Timeout = args.Timeout

	rb := action.NewRollback(actionConfig)
	rb.WaitStrategy = waitStrategy
	rb.Timeout = args.Timeout
	rb.ForceConflicts = args.SSAForceConflicts

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

// httpClientTrustingCABundle returns an *http.Client whose TLS transport
// trusts caBundle (PEM encoded) in addition to the system trust store. It's
// used for the OCI registry client, which does not read a CA file path -
// unlike the classic HTTP(S) chart-repository getter, it only accepts an
// *http.Client (registry.ClientOptHTTPClient).
func httpClientTrustingCABundle(log logging.Logger, caBundle []byte) (*http.Client, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		log.Info("x509.SystemCertPool() unavailable, OCI registry pulls will trust only the supplied CABundle, not the system trust store", "error", err)
		pool = x509.NewCertPool()
	}
	if ok := pool.AppendCertsFromPEM(caBundle); !ok {
		return nil, errors.New(errFailedToParseCABundle)
	}

	transport := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // http.DefaultTransport is always *http.Transport
	transport.TLSClientConfig = &tls.Config{RootCAs: pool}       //nolint:gosec // caller-supplied CA bundle, not skipping verification

	return &http.Client{Transport: transport}, nil
}

// readSystemCABundle returns the contents of the system's CA bundle file and
// the path it was read from, checking $SSL_CERT_FILE first (matching Go's
// own root-loading behavior, and staying composable with the SSL_CERT_FILE-
// based workaround from issue #202) and then systemCACandidates. Returns a
// nil slice and empty path if none is set or readable.
func readSystemCABundle() ([]byte, string) {
	candidates := systemCACandidates
	if f := os.Getenv("SSL_CERT_FILE"); f != "" {
		candidates = append([]string{f}, candidates...)
	}
	for _, p := range candidates {
		b, err := os.ReadFile(p) //nolint:gosec // G304: p is either $SSL_CERT_FILE (operator-controlled env var, same trust boundary as the process itself) or a fixed well-known system path, never Release-supplied input
		if err == nil && len(b) > 0 {
			return b, p
		}
	}
	return nil, ""
}

// writeCABundleToFile writes content to a path under caBundleCacheDir keyed
// by the SHA-256 of content itself, returning that path, for Helm action
// fields (Pull/Install/Upgrade's embedded ChartPathOptions.CaFile) that take
// a file path rather than raw bytes. Content-addressing means repeated calls
// with the same content - the common case, since a Release's caBundle
// rarely changes between reconciles, and NewClient (which calls this) runs
// on every reconcile via Connect - reuse the same file instead of each call
// creating a new one that nothing ever deletes. Also opportunistically
// removes any entry under caBundleCacheDir whose mtime is older than
// caBundleTTL, bounding total accumulation without a background goroutine.
func writeCABundleToFile(content []byte) (string, error) {
	if err := os.MkdirAll(caBundleCacheDir, 0750); err != nil {
		return "", err
	}

	sum := sha256.Sum256(content)
	finalPath := filepath.Join(caBundleCacheDir, hex.EncodeToString(sum[:])+".pem")

	now := time.Now()
	needsWrite := true
	if fi, err := os.Stat(finalPath); err == nil && !fi.IsDir() {
		// Already present with exactly this content - that's what the hash in
		// the filename guarantees. Just bump its mtime so the sweep below
		// doesn't reap a bundle that's still actively in use. If a concurrent
		// call's sweep removed it in the window between this Stat and the
		// Chtimes below (only possible if it had sat unused for a full
		// caBundleTTL), fall through to recreating it below instead of
		// failing this reconcile over a self-healing race.
		switch err := chtimesFn(finalPath, now, now); {
		case err == nil:
			needsWrite = false
		case !os.IsNotExist(err):
			return "", err
		}
	}

	if needsWrite {
		tmp, err := os.CreateTemp(caBundleCacheDir, ".tmp-ca-bundle-*")
		if err != nil {
			return "", err
		}
		tmpName := tmp.Name()
		_, writeErr := tmp.Write(content)
		closeErr := tmp.Close()
		if writeErr != nil {
			_ = os.Remove(tmpName) //nolint:errcheck // best-effort cleanup, writeErr takes precedence
			return "", writeErr
		}
		if closeErr != nil {
			_ = os.Remove(tmpName) //nolint:errcheck // best-effort cleanup, closeErr takes precedence
			return "", closeErr
		}
		// Rename is atomic on the same filesystem, and safe under concurrent
		// reconciles racing to write the same content-addressed path: whichever
		// wins, the result is byte-identical.
		if err := os.Rename(tmpName, finalPath); err != nil {
			_ = os.Remove(tmpName) //nolint:errcheck // best-effort cleanup, err takes precedence
			return "", err
		}
	}

	sweepStaleCABundles(now)
	return finalPath, nil
}

// sweepStaleCABundles best-effort removes entries under caBundleCacheDir
// whose mtime is older than caBundleTTL. Errors are ignored throughout: this
// is opportunistic housekeeping, not something a reconcile should fail over.
func sweepStaleCABundles(now time.Time) {
	entries, err := os.ReadDir(caBundleCacheDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > caBundleTTL {
			_ = os.Remove(filepath.Join(caBundleCacheDir, e.Name())) //nolint:errcheck // best-effort, next sweep retries
		}
	}
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
		err := hc.login(chartUrl, chartRepo, creds, pc.InsecureSkipTLSVerify)
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

func (hc *client) GetLastRelease(name string) (*release.Release, error) {
	r, err := hc.getClient.Run(name)
	if err != nil {
		return nil, err
	}
	rel, ok := r.(*release.Release)
	if !ok {
		return nil, errors.Errorf("unexpected release type %T", r)
	}
	return rel, nil
}

func (hc *client) Install(name string, chrt *chart.Chart, vals map[string]interface{}, patches []ktype.Patch) (*release.Release, error) {
	hc.installClient.ReleaseName = name

	if len(patches) > 0 {
		hc.installClient.PostRenderer = &KustomizationRender{
			patches: patches,
			logger:  hc.log,
		}
	}

	r, err := hc.installClient.Run(chrt, vals)
	if err != nil {
		return nil, err
	}
	rel, ok := r.(*release.Release)
	if !ok {
		return nil, errors.Errorf("unexpected release type %T", r)
	}
	return rel, nil
}

func (hc *client) Upgrade(name string, chrt *chart.Chart, vals map[string]interface{}, patches []ktype.Patch) (*release.Release, error) {
	// Reset values so that source of truth for desired state is always the CR itself
	hc.upgradeClient.ResetValues = true

	if len(patches) > 0 {
		hc.upgradeClient.PostRenderer = &KustomizationRender{
			patches: patches,
			logger:  hc.log,
		}
	}

	r, err := hc.upgradeClient.Run(name, chrt, vals)
	if err != nil {
		return nil, err
	}
	rel, ok := r.(*release.Release)
	if !ok {
		return nil, errors.Errorf("unexpected release type %T", r)
	}
	return rel, nil
}

func (hc *client) Rollback(name string) error {
	return hc.rollbackClient.Run(name)
}

func (hc *client) Uninstall(name string) error {
	_, err := hc.uninstallClient.Run(name)
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

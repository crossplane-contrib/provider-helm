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
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
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
	"helm.sh/helm/v4/pkg/downloader"
	"helm.sh/helm/v4/pkg/getter"
	"helm.sh/helm/v4/pkg/helmpath"
	"helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/registry"
	release "helm.sh/helm/v4/pkg/release/v1"
	repo "helm.sh/helm/v4/pkg/repo/v1"
	"k8s.io/client-go/rest"
	ktype "sigs.k8s.io/kustomize/api/types"

	clusterv1beta1 "github.com/crossplane-contrib/provider-helm/apis/cluster/release/v1beta1"
	namespacedv1beta1 "github.com/crossplane-contrib/provider-helm/apis/namespaced/release/v1beta1"
)

const (
	helmDriverSecret  = "secret"
	chartContentCache = "/tmp/content-cache"
)

// chtimesFn is os.Chtimes, indirected so tests can simulate the file having
// vanished between writeCABundleToFile's Stat and this call (e.g. a
// concurrent sweep) without needing a genuine data race to hit that branch.
var chtimesFn = os.Chtimes

// caBundleCacheDir is where content-addressed CA bundle files are written,
// each keyed by the SHA-256 of its content, so that repeated calls with the
// same content reuse the same file instead of accumulating a new one every
// time. Mutable in tests.
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
	errFailedToPullChart             = "failed to pull chart"
	errFailedToLoadChart             = "failed to load chart"
	errFailedToParseURL              = "failed to parse URL"
	errFailedToLogin                 = "failed to login to registry"
	errUnexpectedOCIUrlTmpl          = "url not prefixed with oci://, got [%s]"
	errDigestNotSupportedForNonOCI   = "digest is only supported for OCI registries"
	errDigestMismatchTmpl            = "conflicting digest input: URL contains @%s but spec.forProvider.chart.digest is %s"
	errVersionMismatchTmpl           = "conflicting version input: URL contains :%s but spec.forProvider.chart.version is %s"
	errChartDigestMismatchTmpl       = "chart downloaded from %s does not match the digest advertised by its repository index"
	errMalformedDigestTmpl           = "malformed chart digest %q"
	errNoChartName                   = "spec.forProvider.chart.name must be specified when URL is empty"
	errNoChartRepository             = "spec.forProvider.chart.repository must be specified when URL is empty"
	errFailedToInitActionConfig      = "failed to initialize helm action configuration"
	errFailedToCreateRegistryClient  = "failed to create registry client"
	errFailedToCreateContentCacheDir = "failed to create chart content cache directory"
	errFailedToResolveRepoIndexTmpl  = "failed to resolve chart from repository index of %q"
	errFailedToParseCABundle         = "failed to parse CA bundle: no PEM certificates found"
	errFailedToWriteCABundle         = "failed to write CA bundle to a temporary file"
	devel                            = ">0.0.0-0"
)

// DeployOptions are the per-deploy inputs to Install and Upgrade. Unlike Args,
// which configure the client once per Connect, these depend on what the
// controller observed in the same reconcile: which labels describe this deploy
// and whether ownership of pre-existing resources still has to be taken.
type DeployOptions struct {
	// TakeOwnership ignores the check for helm annotations and takes ownership
	// of the existing resources.
	TakeOwnership bool
	// Labels are custom release labels stored with the release. On upgrade
	// helm merges them over the previous release's labels.
	Labels map[string]string
}

// Client is the interface to interact with Helm
type Client interface {
	GetLastRelease(release string) (*release.Release, error)
	Install(release string, chart *chart.Chart, vals map[string]interface{}, patches []ktype.Patch, opts DeployOptions) (*release.Release, error)
	Upgrade(release string, chart *chart.Chart, vals map[string]interface{}, patches []ktype.Patch, opts DeployOptions) (*release.Release, error)
	Rollback(release string) error
	Uninstall(release string) error
	PullAndLoadChart(mg resource.Managed, creds *RepoCreds) (*chart.Chart, error)
}

type client struct {
	log             logging.Logger
	getClient       *action.Get
	installClient   *action.Install
	upgradeClient   *action.Upgrade
	rollbackClient  *action.Rollback
	uninstallClient *action.Uninstall
	loginClient     *action.RegistryLogin

	getters               getter.Providers
	registryClient        *registry.Client
	contentCache          string
	insecureSkipTLSVerify bool
	plainHTTP             bool
	caFile                string
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

	rc, err := newRegistryClient(log, args)
	if err != nil {
		return nil, err
	}
	actionConfig.RegistryClient = rc

	// caFile is only set when a CABundle is supplied. It configures the
	// classic HTTP(S) chart-repository getter (via the chart downloader's
	// TLS options and Install/Upgrade's ChartPathOptions, for non-OCI
	// repos). The OCI registry path above does not read this field at all -
	// it goes through its own *registry.Client - so both need to be
	// configured for a CABundle to cover both protocols.
	var caFile string
	if len(args.CABundle) > 0 {
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

	// Charts are cached content-addressed: entries are keyed by the digest
	// resolved from the requesting source, so they cannot collide across
	// sources or be poisoned by charts advertising a foreign name and version.
	if _, err := os.Stat(chartContentCache); os.IsNotExist(err) {
		err = os.Mkdir(chartContentCache, 0750)
		if err != nil {
			return nil, errors.Wrap(err, errFailedToCreateContentCacheDir)
		}
	}

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
	ic.ForceConflicts = args.SSAForceConflicts

	uc := action.NewUpgrade(actionConfig)
	uc.WaitStrategy = waitStrategy
	uc.Timeout = args.Timeout
	uc.SkipCRDs = args.SkipCRDs
	uc.InsecureSkipTLSVerify = args.InsecureSkipTLSVerify
	uc.PlainHTTP = args.PlainHTTP
	uc.CaFile = caFile
	uc.MaxHistory = args.MaxHistory
	uc.ForceConflicts = args.SSAForceConflicts
	if args.SSAForceConflicts {
		// Helm's default "auto" keeps client-side apply for releases whose
		// last revision used it (e.g. installed by Helm v3), and Helm rejects
		// ForceConflicts without server-side apply. Force server-side apply
		// so such releases migrate instead of failing.
		uc.ServerSideApply = "true"
	}

	uic := action.NewUninstall(actionConfig)
	uic.WaitStrategy = waitStrategy
	uic.Timeout = args.Timeout

	rb := action.NewRollback(actionConfig)
	rb.WaitStrategy = waitStrategy
	rb.Timeout = args.Timeout
	rb.ForceConflicts = args.SSAForceConflicts
	if args.SSAForceConflicts {
		// A rollback uses the apply method of the revision it rolls back to,
		// which may predate server-side apply; see the upgrade client above.
		rb.ServerSideApply = "true"
	}

	lc := action.NewRegistryLogin(actionConfig)

	return &client{
		log:             log,
		getClient:       gc,
		installClient:   ic,
		upgradeClient:   uc,
		rollbackClient:  rb,
		uninstallClient: uic,
		loginClient:     lc,

		getters:               getter.All(&cli.EnvSettings{ContentCache: chartContentCache}),
		registryClient:        rc,
		contentCache:          chartContentCache,
		insecureSkipTLSVerify: args.InsecureSkipTLSVerify,
		plainHTTP:             args.PlainHTTP,
		caFile:                caFile,
	}, nil
}

// newRegistryClient builds the OCI registry client that every pull shares
// (actionConfig.RegistryClient). Helm v4's OCI getter uses a preset registry
// client as-is, so the Pull action's PlainHTTP and InsecureSkipTLSVerify
// never reach it: they are applied here, together with the CABundle.
func newRegistryClient(log logging.Logger, args *Args) (*registry.Client, error) {
	opts := []registry.ClientOption{}
	if args.PlainHTTP {
		opts = append(opts, registry.ClientOptPlainHTTP())
	}
	if len(args.CABundle) > 0 || args.InsecureSkipTLSVerify {
		httpClient, err := registryHTTPClient(log, args.CABundle, args.InsecureSkipTLSVerify)
		if err != nil {
			return nil, err
		}
		opts = append(opts, registry.ClientOptHTTPClient(httpClient))
	}
	rc, err := registry.NewClient(opts...)
	if err != nil {
		return nil, errors.Wrap(err, errFailedToCreateRegistryClient)
	}
	return rc, nil
}

// httpClientTrustingCABundle returns an *http.Client whose TLS transport
// trusts caBundle (PEM encoded) in addition to the system trust store. It's
// used for the OCI registry client, which does not read a CA file path -
// unlike the classic HTTP(S) chart-repository getter, it only accepts an
// *http.Client (registry.ClientOptHTTPClient).
func httpClientTrustingCABundle(log logging.Logger, caBundle []byte) (*http.Client, error) {
	return registryHTTPClient(log, caBundle, false)
}

// registryHTTPClient returns the *http.Client for the OCI registry client.
// With a caBundle (PEM encoded), its TLS transport trusts the bundle in
// addition to the system trust store; with insecureSkipVerify, it skips
// certificate verification (spec.forProvider.insecureSkipTLSVerify).
func registryHTTPClient(log logging.Logger, caBundle []byte, insecureSkipVerify bool) (*http.Client, error) {
	var pool *x509.CertPool
	if len(caBundle) > 0 {
		var err error
		pool, err = x509.SystemCertPool()
		if err != nil || pool == nil {
			log.Info("x509.SystemCertPool() unavailable, OCI registry pulls will trust only the supplied CABundle, not the system trust store", "error", err)
			pool = x509.NewCertPool()
		}
		if ok := pool.AppendCertsFromPEM(caBundle); !ok {
			return nil, errors.New(errFailedToParseCABundle)
		}
	}

	transport := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // http.DefaultTransport is always *http.Transport
	transport.TLSClientConfig = &tls.Config{
		RootCAs:            pool,               // nil means the system trust store
		InsecureSkipVerify: insecureSkipVerify, //nolint:gosec // only when the Release asks for insecureSkipTLSVerify
	}

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

// newChartDownloader builds a downloader wired to helm's content-addressed
// chart cache.
func (hc *client) newChartDownloader(creds *RepoCreds) *downloader.ChartDownloader {
	return &downloader.ChartDownloader{
		Out:     io.Discard,
		Verify:  downloader.VerifyNever,
		Getters: hc.getters,
		Options: []getter.Option{
			getter.WithBasicAuth(creds.Username, creds.Password),
			getter.WithInsecureSkipVerifyTLS(hc.insecureSkipTLSVerify),
			getter.WithPlainHTTP(hc.plainHTTP),
			getter.WithTLSClientConfig("", "", hc.caFile),
			getter.WithRegistryClient(hc.registryClient),
		},
		RegistryClient: hc.registryClient,
		ContentCache:   hc.contentCache,
		Cache:          &downloader.DiskCache{Root: hc.contentCache},
	}
}

// pullChart fetches the chart described by the spec through helm's
// content-addressed cache and returns the path of the cached tarball. Cache
// keys are digests resolved from the requesting source at lookup time (the
// OCI registry, or the classic repository index), never reconstructed
// filenames.
func (hc *client) pullChart(chartUrl, chartName, chartVersion, chartRepo, chartDigest string, creds *RepoCreds) (string, error) {
	if creds.Username != "" && creds.Password != "" {
		if err := hc.login(chartUrl, chartRepo, creds); err != nil {
			return "", err
		}
	}

	dl := hc.newChartDownloader(creds)

	switch {
	case registry.IsOCI(chartUrl):
		u, urlVersion, urlDigest, err := resolveOCIChartVersionAndDigest(chartUrl)
		if err != nil {
			return "", err
		}
		effectiveDigest, err := resolveEffectiveDigest(urlDigest, chartDigest)
		if err != nil {
			return "", err
		}
		version, err := resolveEffectiveVersion(urlVersion, chartVersion)
		if err != nil {
			return "", err
		}
		if effectiveDigest != "" {
			// Append digest if present (per Helm PR #12690)
			return hc.fetchOCIRefToCache(dl, u.String()+"@"+effectiveDigest, effectiveDigest)
		}
		return hc.downloadToCache(dl, u.String(), version)
	case chartUrl != "":
		// Direct (non-OCI) URL: nothing advertises a digest up front, so the
		// tarball is downloaded on every pull and cached by its content hash.
		return hc.downloadToCache(dl, chartUrl, "")
	case registry.IsOCI(chartRepo):
		if chartDigest != "" {
			return hc.fetchOCIRefToCache(dl, resolveOCIChartRef(chartRepo, chartName, chartDigest), chartDigest)
		}
		return hc.downloadToCache(dl, resolveOCIChartRef(chartRepo, chartName, ""), chartVersion)
	default:
		return hc.pullRepoChart(dl, chartRepo, chartName, chartVersion, creds)
	}
}

func (hc *client) downloadToCache(dl *downloader.ChartDownloader, ref, version string) (string, error) {
	chartFilePath, _, err := dl.DownloadToCache(ref, version)
	if err != nil {
		return "", errors.Wrap(err, errFailedToPullChart)
	}
	hc.log.Debug("chart resolved through content cache", "ref", ref, "version", version, "path", chartFilePath)
	return chartFilePath, nil
}

// pullRepoChart resolves a chart in a classic repository index and fetches it
// through the content-addressed cache. The index advertises the tarball's
// digest, so a cached chart skips the tarball download entirely.
func (hc *client) pullRepoChart(dl *downloader.ChartDownloader, repoURL, name, version string, creds *RepoCreds) (string, error) {
	if version == devel {
		version = ""
	}

	// The entry name only namespaces the transient index cache files; a
	// random name prevents concurrent reconciles from racing on them,
	// mirroring helm's own FindChartInRepoURL.
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	entry := &repo.Entry{
		Name:                  strings.ReplaceAll(base64.StdEncoding.EncodeToString(buf), "/", "-"),
		URL:                   repoURL,
		Username:              creds.Username,
		Password:              creds.Password,
		InsecureSkipTLSVerify: hc.insecureSkipTLSVerify,
		CAFile:                hc.caFile,
	}
	cr, err := repo.NewChartRepository(entry, hc.getters)
	if err != nil {
		return "", errors.Wrapf(err, errFailedToResolveRepoIndexTmpl, repoURL)
	}
	idxPath, err := cr.DownloadIndexFile()
	if err != nil {
		return "", errors.Wrapf(err, errFailedToResolveRepoIndexTmpl, repoURL)
	}
	defer func() {
		_ = os.RemoveAll(filepath.Join(cr.CachePath, helmpath.CacheChartsFile(cr.Config.Name)))
		_ = os.RemoveAll(filepath.Join(cr.CachePath, helmpath.CacheIndexFile(cr.Config.Name)))
	}()

	idx, err := repo.LoadIndexFile(idxPath)
	if err != nil {
		return "", errors.Wrapf(err, errFailedToResolveRepoIndexTmpl, repoURL)
	}
	cv, err := idx.Get(name, version)
	if err != nil {
		return "", errors.Wrapf(err, errFailedToResolveRepoIndexTmpl, repoURL)
	}
	if len(cv.URLs) == 0 {
		return "", errors.Errorf("chart %q in repository %q has no downloadable URLs", name, repoURL)
	}
	chartURL, err := repo.ResolveReferenceURL(repoURL, cv.URLs[0])
	if err != nil {
		return "", errors.Wrapf(err, errFailedToResolveRepoIndexTmpl, repoURL)
	}

	return hc.fetchURLToCache(dl, chartURL, cv.Digest)
}

// cachedChart returns the content-cache path for a digest-pinned chart when
// it is already cached, letting digest-pinned deploys skip the registry
// round-trip entirely. The Info log doubles as the observable signal for
// end-to-end cache tests, which assert it via the provider logs.
func (hc *client) cachedChart(dl *downloader.ChartDownloader, digest string) (string, bool) {
	key, ok := digestCacheKey(digest)
	if !ok {
		return "", false
	}
	chartFilePath, err := dl.Cache.Get(key, downloader.CacheChart)
	if err != nil {
		return "", false
	}
	hc.log.Info("chart served from content cache", "digest", digest, "path", chartFilePath)
	return chartFilePath, true
}

// fetchOCIRefToCache pulls a digest-pinned OCI chart through the content
// cache, keyed by the pinned manifest digest. Helm's own downloader resolves
// no cache key for install-by-digest references (ValidateReference returns an
// empty hash for them), which would leave every digest-only pull keyed by the
// post-download tarball hash — a key nothing can look up before the next
// download. The tarball bytes cannot be verified against the manifest digest
// (it hashes the OCI manifest, not the tarball); the registry client already
// verifies the pull content-addressably.
func (hc *client) fetchOCIRefToCache(dl *downloader.ChartDownloader, ref, digest string) (string, error) {
	if chartFilePath, ok := hc.cachedChart(dl, digest); ok {
		return chartFilePath, nil
	}
	key, haveKey := digestCacheKey(digest)
	if !haveKey {
		return "", errors.Errorf(errMalformedDigestTmpl, digest)
	}

	u, err := url.Parse(ref)
	if err != nil {
		return "", errors.Wrap(err, errFailedToParseURL)
	}
	g, err := dl.Getters.ByScheme(u.Scheme)
	if err != nil {
		return "", errors.Wrap(err, errFailedToPullChart)
	}
	data, err := g.Get(ref, append(dl.Options, getter.WithURL(ref))...)
	if err != nil {
		return "", errors.Wrap(err, errFailedToPullChart)
	}

	chartFilePath, err := dl.Cache.Put(key, data, downloader.CacheChart)
	return chartFilePath, errors.Wrap(err, errFailedToPullChart)
}

// fetchURLToCache downloads chartURL through the content cache. When the
// source advertises a well-formed sha256 digest, a cached copy is returned
// without any network fetch, and downloaded bytes are verified against the
// advertised digest before entering the cache, so a repository cannot poison
// cache entries it does not itself advertise.
func (hc *client) fetchURLToCache(dl *downloader.ChartDownloader, chartURL, digest string) (string, error) {
	if chartFilePath, ok := hc.cachedChart(dl, digest); ok {
		return chartFilePath, nil
	}
	key, haveKey := digestCacheKey(digest)

	u, err := url.Parse(chartURL)
	if err != nil {
		return "", errors.Wrap(err, errFailedToParseURL)
	}
	g, err := dl.Getters.ByScheme(u.Scheme)
	if err != nil {
		return "", errors.Wrap(err, errFailedToPullChart)
	}
	// WithURL marks chartURL as the URL the basic auth credentials belong to;
	// without it the http getter withholds them from every request. This
	// mirrors what helm's downloader sets for a ref without an owning
	// repositories.yaml entry.
	data, err := g.Get(chartURL, append(dl.Options, getter.WithURL(chartURL))...)
	if err != nil {
		return "", errors.Wrap(err, errFailedToPullChart)
	}

	sum := sha256.Sum256(data.Bytes())
	if haveKey && sum != key {
		return "", errors.Errorf(errChartDigestMismatchTmpl, chartURL)
	}
	chartFilePath, err := dl.Cache.Put(sum, data, downloader.CacheChart)
	return chartFilePath, errors.Wrap(err, errFailedToPullChart)
}

// digestCacheKey converts an advertised sha256 digest, with or without the
// "sha256:" prefix, into a content cache key.
func digestCacheKey(digest string) ([sha256.Size]byte, bool) {
	var key [sha256.Size]byte
	sum, err := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	if err != nil || len(sum) != sha256.Size {
		return key, false
	}
	copy(key[:], sum)
	return key, true
}

// login logs in to an OCI registry before a pull. Helm v4's RegistryLogin.Run
// always sets the shared registry client's plainHTTP from WithPlainHTTPLogin,
// so plainHTTP must be passed here too: otherwise the login goes over HTTPS
// and the pull that follows does as well.
func (hc *client) login(chartUrl, chartRepo string, creds *RepoCreds) error {
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
	err = hc.loginClient.Run(&out, parsedURL.Host, creds.Username, creds.Password, action.WithInsecure(hc.insecureSkipTLSVerify), action.WithPlainHTTPLogin(hc.plainHTTP))
	hc.log.Debug(out.String())
	return errors.Wrap(err, errFailedToLogin)
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

// resolveEffectiveVersion reconciles the version embedded in an OCI chart URL
// with spec.forProvider.chart.version, mirroring resolveEffectiveDigest.
// Conflicting values are rejected rather than silently resolved in favor of
// the URL. A devel spec version is treated as unset since it does not pin a
// concrete version.
func resolveEffectiveVersion(urlVersion, specVersion string) (string, error) {
	if specVersion == devel {
		specVersion = ""
	}
	if specVersion != "" && urlVersion != "" && urlVersion != specVersion {
		return "", errors.Errorf(errVersionMismatchTmpl, urlVersion, specVersion)
	}
	if urlVersion != "" {
		return urlVersion, nil
	}
	return specVersion, nil
}

// URLPullsSpecVersion reports whether a deploy from chartURL selects the chart
// by spec.forProvider.chart.version, so that the deployed chart version is
// expected to match it. That holds only for an OCI URL without an embedded tag
// or digest and without specDigest: a tag or digest selects the chart on its
// own (a digest-pinned pull ignores the version entirely), and a non-OCI URL
// points at a fixed package.
func URLPullsSpecVersion(chartURL, specDigest string) bool {
	if specDigest != "" || !registry.IsOCI(chartURL) {
		return false
	}
	_, urlVersion, urlDigest, err := resolveOCIChartVersionAndDigest(chartURL)
	return err == nil && urlVersion == "" && urlDigest == ""
}

// URLVersionConflict returns the error a deploy fails with when chartURL is an
// OCI URL embedding a version that conflicts with specVersion, and nil
// otherwise. Helm rejects such a pull itself ("chart reference and version
// mismatch"), so the spec can never deploy. A URL that does not parse is left
// to the deploy, which reports its own error.
func URLVersionConflict(chartURL, specVersion string) error {
	if !registry.IsOCI(chartURL) {
		return nil
	}
	_, urlVersion, _, err := resolveOCIChartVersionAndDigest(chartURL)
	if err != nil {
		return nil //nolint:nilerr // the deploy reports its own error for a URL that does not parse
	}
	_, err = resolveEffectiveVersion(urlVersion, specVersion)
	return err
}

func (hc *client) PullAndLoadChart(mg resource.Managed, creds *RepoCreds) (*chart.Chart, error) {
	var chartUrl, chartName, chartVersion, chartDigest, chartRepo string

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

	// Validate: Digest only works with OCI registries. A set URL is the sole
	// pull source, so an OCI Repository next to a non-OCI URL does not count.
	if chartDigest != "" {
		digestSource := chartUrl
		if digestSource == "" {
			digestSource = chartRepo
		}
		if !registry.IsOCI(digestSource) {
			return nil, errors.New(errDigestNotSupportedForNonOCI)
		}
	}

	// Validate: without a URL the chart can only be resolved from
	// Repository + Name. This must run before any pull so that
	// misconfiguration fails with a clear error instead of an opaque helm one.
	if chartUrl == "" {
		switch {
		case chartName == "":
			return nil, errors.New(errNoChartName)
		case chartRepo == "":
			return nil, errors.New(errNoChartRepository)
		}
	}

	chartFilePath, err := hc.pullChart(chartUrl, chartName, chartVersion, chartRepo, chartDigest, creds)
	if err != nil {
		return nil, err
	}

	chart, err := loader.Load(chartFilePath)
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

func (hc *client) Install(name string, chrt *chart.Chart, vals map[string]interface{}, patches []ktype.Patch, opts DeployOptions) (*release.Release, error) {
	hc.installClient.ReleaseName = name
	hc.installClient.TakeOwnership = opts.TakeOwnership
	hc.installClient.Labels = opts.Labels

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

func (hc *client) Upgrade(name string, chrt *chart.Chart, vals map[string]interface{}, patches []ktype.Patch, opts DeployOptions) (*release.Release, error) {
	// Reset values so that source of truth for desired state is always the CR itself
	hc.upgradeClient.ResetValues = true
	hc.upgradeClient.TakeOwnership = opts.TakeOwnership
	hc.upgradeClient.Labels = opts.Labels

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

func resolveOCIChartRef(repository, name, digest string) string {
	ref := strings.Join([]string{strings.TrimSuffix(repository, "/"), name}, "/")
	if d := strings.TrimSpace(digest); d != "" {
		ref += "@" + d
	}
	return ref
}

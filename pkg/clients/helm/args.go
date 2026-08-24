package helm

import "time"

// Args stores common options that can be passed to a Helm client on initialization
type Args struct {
	// Namespace to install the release into.
	Namespace string
	// Wait for the release to become ready.
	Wait bool
	// Timeout is the duration Helm will wait for the release to become ready.
	Timeout time.Duration
	// SkipCRDs skips CRDs creation during Helm release install or upgrade.
	SkipCRDs bool
	// InsecureSkipTLSVerify skips tls certificate checks for the chart download
	InsecureSkipTLSVerify bool
	// PlainHTTP uses HTTP connections for the chart download
	PlainHTTP bool
	// CABundle is a PEM encoded CA bundle used to verify the chart
	// repository or registry's TLS certificate, in addition to the system
	// trust store. Empty means only the system trust store is used. OCI
	// registry pulls always include the system trust store (via
	// crypto/x509.SystemCertPool); classic HTTP(S) chart-repo pulls include
	// it best-effort, by reading a well-known system CA bundle file path
	// (or $SSL_CERT_FILE if set) - if none is found, only CABundle is
	// trusted for that path.
	CABundle []byte
	// TakeOwnership ignore the check for helm annotations and take ownership
	// of the existing resources.
	TakeOwnership bool
	// MaxHistory limits the maximum number of revisions saved per release. Use 0 for no limit.
	MaxHistory int
	// SSAForceConflicts forces Kubernetes server-side apply to overwrite
	// field conflicts ("become sole manager") on install, upgrade, and
	// rollback.
	SSAForceConflicts bool
}

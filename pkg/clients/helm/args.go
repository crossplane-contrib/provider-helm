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
}

package helm

// RepoCreds keeps auth information to access a Helm Chart
type RepoCreds struct {
	Username string
	Password string //nolint:gosec // G117: Password field is intentionally exported for internal use, not serialized to JSON
}

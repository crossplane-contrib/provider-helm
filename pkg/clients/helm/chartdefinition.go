package helm

// ChartDefinition keeps the required information to access a Helm Chart
type ChartDefinition struct {
	Repository string
	Name       string
	Version    string
	RepoUser   string
	RepoPass   string
}

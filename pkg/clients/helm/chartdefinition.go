package helmClient

// A ChartSpec defines the chart spec for a Release
type ChartDefinition struct {
	Repository string
	Name       string
	Version    string
	RepoUser   string
	RepoPass   string
}

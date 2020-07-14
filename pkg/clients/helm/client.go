package helmClient

import (
	"fmt"
	"os"
	"path/filepath"

	"helm.sh/helm/v3/pkg/cli"

	"k8s.io/client-go/rest"

	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/downloader"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/release"
)

const (
	helmDriverSecret = "secret"
	chartCache       = "/tmp/charts"
)

type Client interface {
	FetchChart(repo, name, version, username, password string) (*chart.Chart, error)
	GetLastRelease(release string) (*release.Release, error)
	Install(release string, chart *chart.Chart, vals map[string]interface{}) (*release.Release, error)
	Upgrade(release string, chart *chart.Chart, vals map[string]interface{}) (*release.Release, error)
	Rollback(release string) error
	Uninstall(release string) error
}

type client struct {
	histClient      *action.History
	installClient   *action.Install
	upgradeClient   *action.Upgrade
	rollbackClient  *action.Rollback
	uninstallClient *action.Uninstall
}

func NewClient(log logging.Logger, config *rest.Config, namespace string) (Client, error) {
	rg := newRESTClientGetter(config, namespace)

	actionConfig := new(action.Configuration)
	// Always store helm state in the same cluster/namespace where chart is deployed
	if err := actionConfig.Init(rg, namespace, helmDriverSecret, func(format string, v ...interface{}) {
		log.Debug(fmt.Sprintf(format, v))
	}); err != nil {
		return nil, err
	}

	hc := action.NewHistory(actionConfig)
	hc.Max = 1

	ic := action.NewInstall(actionConfig)
	ic.Namespace = namespace

	uc := action.NewUpgrade(actionConfig)
	uic := action.NewUninstall(actionConfig)

	rb := action.NewRollback(actionConfig)

	return &client{
		histClient:      hc,
		installClient:   ic,
		upgradeClient:   uc,
		rollbackClient:  rb,
		uninstallClient: uic,
	}, nil
}

func (hc *client) FetchChart(repo, name, version, username, password string) (*chart.Chart, error) {
	cd := downloader.ChartDownloader{
		Out:     os.Stderr,
		Verify:  downloader.VerifyNever,
		Getters: getter.All(&cli.EnvSettings{}),
	}
	if username != "" && password != "" {
		cd.Options = append(cd.Options, getter.WithBasicAuth(username, password))
	}

	n := fmt.Sprintf("%s-%s", name, version)
	fn := fmt.Sprintf("%s.tgz", n)
	chartURL := fmt.Sprintf("%s/%s", repo, fn)

	if _, err := os.Stat(chartCache); os.IsNotExist(err) {
		err = os.Mkdir(chartCache, 0750)
		if err != nil {
			return nil, err
		}
	}
	ef := filepath.Join(chartCache, fn)
	if _, err := os.Stat(ef); os.IsNotExist(err) {
		f, _, err := cd.DownloadTo(chartURL, "", chartCache)
		if err != nil {
			return nil, err
		}
		if f != ef {
			return nil, errors.New(fmt.Sprintf("chart file was not cached to expected path, expecting %s, actual %s", ef, f))
		}
	} else if err != nil {
		return nil, errors.Wrap(err, "failed to check if cached chart file exists")
	}

	chart, err := loader.Load(ef)
	if err != nil {
		return nil, err
	}
	return chart, nil
}

func (hc *client) GetLastRelease(release string) (*release.Release, error) {
	rels, err := hc.histClient.Run(release)
	if err != nil {
		return nil, err
	}
	nl := len(rels)
	if nl < 1 {
		return nil, errors.New("number of releases is less than 1 for an existing release")
	}
	// Get newest release
	rel := rels[nl-1]
	return rel, nil
}

func (hc *client) Install(release string, chart *chart.Chart, vals map[string]interface{}) (*release.Release, error) {
	hc.installClient.ReleaseName = release
	return hc.installClient.Run(chart, vals)
}

func (hc *client) Upgrade(release string, chart *chart.Chart, vals map[string]interface{}) (*release.Release, error) {
	return hc.upgradeClient.Run(release, chart, vals)
}

func (hc *client) Rollback(release string) error {
	return hc.rollbackClient.Run(release)
}

func (hc *client) Uninstall(release string) error {
	_, err := hc.uninstallClient.Run(release)
	return err
}

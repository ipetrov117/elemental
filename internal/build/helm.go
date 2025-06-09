package build

import (
	"github.com/suse/elemental/v3/pkg/manifest/api"
)

const (
	helmChartAPIVersion = "helm.cattle.io/v1"
	helmChartKind       = "HelmChart"
	helmChartSource     = "edge-image-builder"
	helmBackoffLimit    = 20
	kubeSystemNamespace = "kube-system"
)

type HelmCRD struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace,omitempty"`
	} `yaml:"metadata"`
	Spec struct {
		Version         string `yaml:"version"`
		ValuesContent   string `yaml:"valuesContent,omitempty"`
		Repo            string `yaml:"repo,omitempty"`
		Chart           string `yaml:"chart,omitempty"`
		TargetNamespace string `yaml:"targetNamespace,omitempty"`
		CreateNamespace bool   `yaml:"createNamespace,omitempty"`
		BackOffLimit    int    `yaml:"backOffLimit"`
	} `yaml:"spec"`
}

func NewHelmCRD(chart *api.HelmChart, repositoryURL string) *HelmCRD {
	return &HelmCRD{
		APIVersion: helmChartAPIVersion,
		Kind:       helmChartKind,
		Metadata: struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace,omitempty"`
		}{
			Name:      chart.Chart,
			Namespace: kubeSystemNamespace,
		},
		Spec: struct {
			Version         string `yaml:"version"`
			ValuesContent   string `yaml:"valuesContent,omitempty"`
			Repo            string `yaml:"repo,omitempty"`
			Chart           string `yaml:"chart,omitempty"`
			TargetNamespace string `yaml:"targetNamespace,omitempty"`
			CreateNamespace bool   `yaml:"createNamespace,omitempty"`
			BackOffLimit    int    `yaml:"backOffLimit"`
		}{
			Version:         chart.Version,
			ValuesContent:   chart.Values,
			TargetNamespace: chart.Namespace,
			Repo:            repositoryURL,
			Chart:           chart.Chart,
			CreateNamespace: true,
			BackOffLimit:    helmBackoffLimit,
		},
	}
}

func ProduceCRDs(helm *api.Helm) []*HelmCRD {
	repoMap := map[string]string{}

	for _, repo := range helm.Repositories {
		repoMap[repo.Name] = repo.URL
	}

	chartCRDs := []*HelmCRD{}
	for _, helmChart := range helm.Charts {
		chartCRDs = append(chartCRDs, NewHelmCRD(&helmChart, repoMap[helmChart.Repository]))
	}

	return chartCRDs
}

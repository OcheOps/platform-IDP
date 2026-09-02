package gitops

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
)

var serviceName = regexp.MustCompile(`^[a-z](?:[a-z0-9-]{0,38}[a-z0-9])?$`)
var digest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

type Work struct {
	Service, Owner, Template, TemplateVersion, ImageRepository, ImageDigest string
	Environment, BaseDomain, DatabaseSecretName, RequestID                  string
	CPU, Memory                                                             string
	MinReplicas, MaxReplicas, TargetCPU                                     int
	Database                                                                bool
}

type Values struct {
	Service    Service     `json:"service"`
	Image      Image       `json:"image"`
	Deployment Deployment  `json:"deployment"`
	Ingress    Ingress     `json:"ingress"`
	Database   Database    `json:"database"`
	Secrets    []SecretRef `json:"secrets"`
}
type Service struct {
	Name        string `json:"name"`
	Owner       string `json:"owner"`
	Environment string `json:"environment"`
}
type Image struct {
	Repository string `json:"repository"`
	Digest     string `json:"digest"`
}
type Deployment struct {
	Port        int         `json:"port"`
	Health      Health      `json:"health"`
	Resources   Resources   `json:"resources"`
	Autoscaling Autoscaling `json:"autoscaling"`
}
type Health struct {
	LivePath  string `json:"livePath"`
	ReadyPath string `json:"readyPath"`
}
type Resources struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}
type Autoscaling struct {
	MinReplicas          int `json:"minReplicas"`
	MaxReplicas          int `json:"maxReplicas"`
	TargetCPUUtilization int `json:"targetCPUUtilization"`
}
type Ingress struct {
	Hostname string `json:"hostname"`
}
type Database struct {
	Engine           string `json:"engine"`
	SecretName       string `json:"secretName,omitempty"`
	MigrationCommand string `json:"migrationCommand,omitempty"`
}
type SecretRef struct {
	EnvironmentVariable string `json:"environmentVariable"`
	SecretName          string `json:"secretName"`
	Key                 string `json:"key"`
	RemoteKey           string `json:"remoteKey"`
}

func RenderValues(work Work) ([]byte, error) {
	if !serviceName.MatchString(work.Service) || work.Environment != "staging" || work.Owner == "" || work.Template == "" || work.TemplateVersion == "" || work.RequestID == "" {
		return nil, errors.New("GitOps service identity is invalid")
	}
	if !digest.MatchString(work.ImageDigest) || work.ImageRepository == "" {
		return nil, errors.New("an immutable image repository and digest are required")
	}
	if work.CPU == "" || work.Memory == "" || work.MinReplicas < 1 || work.MaxReplicas < work.MinReplicas || work.MaxReplicas > 10 || work.TargetCPU < 40 || work.TargetCPU > 90 {
		return nil, errors.New("deployment capacity is invalid")
	}
	if work.BaseDomain == "" {
		return nil, errors.New("base domain is required")
	}
	values := Values{
		Service: Service{work.Service, work.Owner, work.Environment}, Image: Image{work.ImageRepository, work.ImageDigest},
		Deployment: Deployment{Port: 8080, Health: Health{"/health/live", "/health/ready"}, Resources: Resources{work.CPU, work.Memory}, Autoscaling: Autoscaling{work.MinReplicas, work.MaxReplicas, work.TargetCPU}},
		Ingress:    Ingress{work.Service + ".staging." + work.BaseDomain}, Database: Database{Engine: "none"}, Secrets: []SecretRef{},
	}
	if work.Database {
		if work.DatabaseSecretName == "" {
			return nil, errors.New("database secret name is required")
		}
		values.Database = Database{Engine: "postgres", SecretName: work.Service + "-db", MigrationCommand: "/migrate"}
		values.Secrets = append(values.Secrets, SecretRef{"DATABASE_URL", work.Service + "-db", "DATABASE_URL", work.DatabaseSecretName})
	}
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode GitOps values: %w", err)
	}
	return append(data, '\n'), nil
}

func RenderApplicationSet(repositoryURL string) ([]byte, error) {
	parsed, err := url.Parse(repositoryURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("ApplicationSet repository URL must be an HTTPS URL without credentials")
	}
	manifest := `apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: platform-services
  namespace: argocd
spec:
  generators:
    - git:
        repoURL: %s
        revision: main
        directories:
          - path: services/*/staging
  template:
    metadata:
      name: '{{path[1]}}-staging'
    spec:
      project: default
      source:
        repoURL: %s
        targetRevision: main
        path: charts/service
        helm:
          valueFiles:
            - '{{path}}/values.json'
      destination:
        server: https://kubernetes.default.svc
        namespace: 'svc-{{path[1]}}'
      syncPolicy:
        automated:
          prune: true
          selfHeal: true
        syncOptions:
          - CreateNamespace=true
`
	return []byte(fmt.Sprintf(manifest, repositoryURL, repositoryURL)), nil
}

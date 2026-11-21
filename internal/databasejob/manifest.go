package databasejob

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/OcheOps/platform-IDP/internal/databaseworker"
)

var (
	imagePattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9.\-/:]*@sha256:[a-f0-9]{64}$`)
	servicePattern  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,38}[a-z0-9]$`)
	databasePattern = regexp.MustCompile(`^svc_[a-z0-9_]+$`)
	userPattern     = regexp.MustCompile(`^usr_[a-z0-9_]+$`)
)

// Config mirrors the values of charts/database-provisioner that are fixed per
// installation. The Job and NetworkPolicy rendered here must stay equivalent to
// that chart.
type Config struct {
	Namespace, ServiceAccount, Image, AWSRegion, VPCCIDR string
}

func (config Config) Validate() error {
	if config.Namespace == "" || config.ServiceAccount == "" || config.AWSRegion == "" {
		return errors.New("database Job namespace, service account, and AWS region are required")
	}
	if !imagePattern.MatchString(config.Image) {
		return errors.New("database provisioner image must be pinned by sha256 digest")
	}
	if _, err := netip.ParsePrefix(config.VPCCIDR); err != nil {
		return errors.New("database Job VPC CIDR is invalid")
	}
	return nil
}

// Name matches the database-provisioner.name chart helper.
func Name(serviceName, requestID string) string {
	sum := sha256.Sum256([]byte(requestID))
	name := "database-" + serviceName + "-" + hex.EncodeToString(sum[:])[:8]
	if len(name) > 63 {
		name = name[:63]
	}
	return strings.TrimRight(name, "-")
}

func validateWork(work databaseworker.Work) error {
	if !servicePattern.MatchString(work.ServiceName) {
		return errors.New("database Job service name is invalid")
	}
	if work.RequestID == "" || len(work.RequestID) > 63 || strings.ContainsAny(work.RequestID, " /\"") {
		return errors.New("database Job request ID is invalid")
	}
	if !databasePattern.MatchString(work.DatabaseName) || !userPattern.MatchString(work.DatabaseUser) {
		return errors.New("database Job database identities are invalid")
	}
	if work.DatabaseHost == "" || work.MasterSecretName == "" || work.PasswordSecretName == "" || work.SecretName == "" || work.PasswordSecretName == work.SecretName {
		return errors.New("database Job connection or secret identities are incomplete")
	}
	if work.DatabasePort < 1 || work.DatabasePort > 65535 {
		return errors.New("database Job port is invalid")
	}
	return nil
}

func jobManifest(config Config, work databaseworker.Work) map[string]any {
	name := Name(work.ServiceName, work.RequestID)
	env := []map[string]string{
		{"name": "AWS_EC2_METADATA_DISABLED", "value": "true"},
		{"name": "AWS_REGION", "value": config.AWSRegion},
		{"name": "REQUEST_ID", "value": work.RequestID},
		{"name": "DATABASE_NAME", "value": work.DatabaseName},
		{"name": "DATABASE_USER", "value": work.DatabaseUser},
		{"name": "DATABASE_HOST", "value": work.DatabaseHost},
		{"name": "DATABASE_PORT", "value": strconv.Itoa(work.DatabasePort)},
		{"name": "MASTER_SECRET_NAME", "value": work.MasterSecretName},
		{"name": "PASSWORD_SECRET_NAME", "value": work.PasswordSecretName},
		{"name": "SERVICE_SECRET_NAME", "value": work.SecretName},
	}
	return map[string]any{
		"apiVersion": "batch/v1", "kind": "Job",
		"metadata": map[string]any{"name": name, "namespace": config.Namespace, "labels": labels(work, true)},
		"spec": map[string]any{
			"backoffLimit": 3, "activeDeadlineSeconds": 360, "ttlSecondsAfterFinished": 3600,
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels(work, false)},
				"spec": map[string]any{
					"restartPolicy": "Never", "serviceAccountName": config.ServiceAccount, "automountServiceAccountToken": false,
					"securityContext": map[string]any{"runAsNonRoot": true, "seccompProfile": map[string]string{"type": "RuntimeDefault"}},
					"containers": []map[string]any{{
						"name": "provisioner", "image": config.Image, "imagePullPolicy": "IfNotPresent",
						"securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "capabilities": map[string]any{"drop": []string{"ALL"}}},
						"env":             env,
						"resources": map[string]any{
							"requests": map[string]string{"cpu": "25m", "memory": "64Mi"},
							"limits":   map[string]string{"cpu": "250m", "memory": "128Mi"},
						},
					}},
				},
			},
		},
	}
}

func networkPolicyManifest(config Config, work databaseworker.Work) map[string]any {
	vpc := map[string]any{"ipBlock": map[string]string{"cidr": config.VPCCIDR}}
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": map[string]any{"name": Name(work.ServiceName, work.RequestID), "namespace": config.Namespace, "labels": labels(work, true)},
		"spec": map[string]any{
			"podSelector": map[string]any{"matchLabels": labels(work, false)},
			"policyTypes": []string{"Egress"},
			"egress": []map[string]any{
				{
					"to":    []map[string]any{{"namespaceSelector": map[string]any{}, "podSelector": map[string]any{"matchLabels": map[string]string{"k8s-app": "kube-dns"}}}},
					"ports": []map[string]any{{"protocol": "UDP", "port": 53}, {"protocol": "TCP", "port": 53}},
				},
				{"to": []map[string]any{vpc}, "ports": []map[string]any{{"protocol": "TCP", "port": work.DatabasePort}}},
				{"to": []map[string]any{vpc}, "ports": []map[string]any{{"protocol": "TCP", "port": 443}}},
			},
		},
	}
}

func labels(work databaseworker.Work, object bool) map[string]string {
	values := map[string]string{"app.kubernetes.io/name": "database-provisioner", "platform.idp/request-id": work.RequestID}
	if object {
		values["app.kubernetes.io/part-of"] = "platform-idp"
	}
	return values
}

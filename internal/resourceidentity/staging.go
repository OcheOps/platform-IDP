package resourceidentity

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var dnsLabel = regexp.MustCompile(`^[a-z](?:[a-z0-9-]{0,38}[a-z0-9])$`)
var accountID = regexp.MustCompile(`^[0-9]{12}$`)

type StagingResources struct {
	CanonicalID         string `json:"canonicalId"`
	ECRRepository       string `json:"ecrRepository"`
	DatabaseSecretName  string `json:"databaseSecretName,omitempty"`
	SecretStoreRoleName string `json:"secretStoreRoleName,omitempty"`
	SecretStoreRoleARN  string `json:"secretStoreRoleArn,omitempty"`
}

func Staging(installation, service, account string, database bool) (StagingResources, error) {
	if !dnsLabel.MatchString(installation) || !dnsLabel.MatchString(service) || !accountID.MatchString(account) {
		return StagingResources{}, errors.New("staging resource identity is invalid")
	}
	canonical := installation + "/" + service + "/staging"
	resources := StagingResources{CanonicalID: canonical, ECRRepository: "platform/" + service}
	if !database {
		return resources, nil
	}
	resources.DatabaseSecretName = "platform/" + canonical + "/database"
	resources.SecretStoreRoleName = hashedName(installation+"-"+service+"-staging-secrets", canonical)
	resources.SecretStoreRoleARN = "arn:aws:iam::" + account + ":role/" + resources.SecretStoreRoleName
	return resources, nil
}

func hashedName(base, canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	if len(base) > 55 {
		base = strings.TrimSuffix(base[:55], "-")
	}
	return fmt.Sprintf("%s-%x", base, sum[:4])
}

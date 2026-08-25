package goldenpath

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type GoChiInputs struct {
	ServiceName          string
	Description          string
	Owner                string
	GitHubOrganization   string
	ModulePath           string
	AWSRegion            string
	ECRRepository        string
	GitHubActionsRoleARN string
}

var (
	serviceNamePattern   = regexp.MustCompile(`^[a-z](?:[a-z0-9-]{0,38}[a-z0-9])?$`)
	ownerPattern         = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)
	modulePathPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]*(?:/[A-Za-z0-9][A-Za-z0-9._~-]*)+$`)
	regionPattern        = regexp.MustCompile(`^[a-z]{2}(?:-gov)?-[a-z]+-[0-9]$`)
	roleARNPattern       = regexp.MustCompile(`^arn:aws(?:-us-gov)?:iam::[0-9]{12}:role/[A-Za-z0-9+=,.@_/-]+$`)
	ecrRepositoryPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._/-][a-z0-9]+)*$`)
)

func (input GoChiInputs) Validate() error {
	var problems []string
	if !serviceNamePattern.MatchString(input.ServiceName) {
		problems = append(problems, "serviceName must be a lowercase DNS label of at most 40 characters")
	}
	if strings.TrimSpace(input.Description) == "" || len(input.Description) > 200 {
		problems = append(problems, "description must contain 1-200 characters")
	}
	if !ownerPattern.MatchString(input.Owner) {
		problems = append(problems, "owner must be a valid GitHub team slug")
	}
	if !ownerPattern.MatchString(input.GitHubOrganization) {
		problems = append(problems, "githubOrganization must be a valid GitHub organization slug")
	}
	if !modulePathPattern.MatchString(input.ModulePath) {
		problems = append(problems, "modulePath must be a valid Go module path")
	}
	if !regionPattern.MatchString(input.AWSRegion) {
		problems = append(problems, "awsRegion must be a valid AWS region")
	}
	if len(input.ECRRepository) > 256 || !ecrRepositoryPattern.MatchString(input.ECRRepository) {
		problems = append(problems, "ecrRepository must be a valid ECR repository name")
	}
	if !roleARNPattern.MatchString(input.GitHubActionsRoleARN) {
		problems = append(problems, "githubActionsRoleArn must be an AWS IAM role ARN")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func RenderGoChi(sourceDirectory, targetDirectory string, input GoChiInputs) error {
	if err := input.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(targetDirectory), 0o755); err != nil {
		return fmt.Errorf("create target parent: %w", err)
	}
	if err := os.Mkdir(targetDirectory, 0o755); err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("refusing to overwrite existing target %s", targetDirectory)
		}
		return fmt.Errorf("create target: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(targetDirectory)
		}
	}()

	replacements := strings.NewReplacer(
		"github.com/platform-idp/example-go-api", input.ModulePath,
		"arn:aws:iam::123456789012:role/example-github-actions", input.GitHubActionsRoleARN,
		"Example Go API service.", jsonStringContent(input.Description),
		"Example Go API", input.ServiceName,
		"example-ecr-repository", input.ECRRepository,
		"example-go-api", input.ServiceName,
		"platform-team", input.Owner,
		"example-org", input.GitHubOrganization,
		"eu-west-1", input.AWSRegion,
	)
	err := filepath.WalkDir(sourceDirectory, func(sourcePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(sourceDirectory, sourcePath)
		if err != nil || relative == "." {
			return err
		}
		targetPath := filepath.Join(targetDirectory, relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("template symlinks are not supported: %s", relative)
		}
		if entry.IsDir() {
			return os.Mkdir(targetPath, 0o755)
		}
		data, err := os.ReadFile(sourcePath)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(targetPath, []byte(replacements.Replace(string(data))), info.Mode().Perm())
	})
	if err != nil {
		return fmt.Errorf("render Go Chi template: %w", err)
	}
	complete = true
	return nil
}

func jsonStringContent(value string) string {
	quoted := strconv.Quote(value)
	return quoted[1 : len(quoted)-1]
}

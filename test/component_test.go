package test

import (
	"context"
	"testing"
	"fmt"
	"strings"
	"os"
	// "time"
	"github.com/cloudposse/test-helpers/pkg/atmos"
	"github.com/cloudposse/test-helpers/pkg/helm"
	awsHelper "github.com/cloudposse/test-helpers/pkg/aws"
	helper "github.com/cloudposse/test-helpers/pkg/atmos/component-helper"
	awsTerratest "github.com/gruntwork-io/terratest/modules/aws"
	"github.com/gruntwork-io/terratest/modules/random"
	"github.com/google/go-github/v70/github"
	"github.com/stretchr/testify/assert"

	// metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// corev1 "k8s.io/api/core/v1"
	// "k8s.io/apimachinery/pkg/runtime/schema"
	// "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	// "k8s.io/client-go/dynamic"
	// "k8s.io/client-go/dynamic/dynamicinformer"
	// "k8s.io/client-go/tools/cache"
)

type ComponentSuite struct {
	helper.TestSuite
}

func (s *ComponentSuite) TestBasic() {
	const component = "eks/actions-runner-controller/basic"
	const stack = "default-test"
	const awsRegion = "us-east-2"

	clusterOptions := s.GetAtmosOptions("eks/cluster", stack, nil)
	clusrerId := atmos.Output(s.T(), clusterOptions, "eks_cluster_id")
	cluster := awsHelper.GetEksCluster(s.T(), context.Background(), awsRegion, clusrerId)
	clientset, err := awsHelper.NewK8SClientset(cluster)
	assert.NoError(s.T(), err)
	assert.NotNil(s.T(), clientset)

	githubOrg := "cloudposse-tests"

	token := os.Getenv("GITHUB_TOKEN")

	randomID := strings.ToLower(random.UniqueId())

	namespace := fmt.Sprintf("external-secrets-%s", randomID)
	secretPathPrefix := fmt.Sprintf("test-%s", randomID)
	secretGithubPATPath := fmt.Sprintf("/%s/token", secretPathPrefix)
	secretWebhookPath := fmt.Sprintf("/%s/webhook", secretPathPrefix)
	secretDockerConfigPath := fmt.Sprintf("/%s/docker", secretPathPrefix)

	defer func() {
		awsTerratest.DeleteParameter(s.T(), awsRegion, secretGithubPATPath)
		awsTerratest.DeleteParameter(s.T(), awsRegion, secretWebhookPath)
		awsTerratest.DeleteParameter(s.T(), awsRegion, secretDockerConfigPath)
	}()
	awsTerratest.PutParameter(s.T(), awsRegion, secretGithubPATPath, "Test value", token)
	awsTerratest.PutParameter(s.T(), awsRegion, secretWebhookPath, "Test value", randomID)
	awsTerratest.PutParameter(s.T(), awsRegion, secretDockerConfigPath, "Test value", randomID)

	inputs := map[string]interface{}{
		"kubernetes_namespace": namespace,
		"ssm_github_secret_path": secretGithubPATPath,
		"ssm_github_webhook_secret_token_path": secretWebhookPath,
		"ssm_docker_config_json_path": secretDockerConfigPath,
		"runners": map[string]interface{}{
			"infra-runner": map[string]interface{}{
				"node_selector": map[string]interface{}{
					"kubernetes.io/os": "linux",
					"kubernetes.io/arch": "amd64",
				},
				"type": "organization",
				"dind_enabled": true,
				"image": "summerwind/actions-runner-dind",
				"scope": githubOrg,
				"min_replicas": 1,
				"max_replicas": 1,
				"scale_down_delay_seconds": 100,
				"scheduled_overrides": []map[string]interface{}{},
				"resources": map[string]interface{}{
					"limits": map[string]interface{}{
						"cpu": "200m",
						"memory": "512Mi",
					},
					"requests": map[string]interface{}{
						"cpu": "100m",
						"memory": "128Mi",
					},
				},
				"webhook_driven_scaling_enabled": false,
				"max_duration": "90m",
				"pull_driven_scaling_enabled": true,
				"labels": []string{
					randomID,
				},
			},
		},
	}

	defer s.DestroyAtmosComponent(s.T(), component, stack, &inputs)
	options, _ := s.DeployAtmosComponent(s.T(), component, stack, &inputs)
	assert.NotNil(s.T(), options)


	metadataArray := []helm.Metadata{}

	atmos.OutputStruct(s.T(), options, "metadata", &metadataArray)

	metadata := metadataArray[0]

	assert.Equal(s.T(), metadata.AppVersion, "0.27.6")
	assert.Equal(s.T(), metadata.Chart, "actions-runner-controller")
	assert.NotNil(s.T(), metadata.FirstDeployed)
	assert.NotNil(s.T(), metadata.LastDeployed)
	assert.Equal(s.T(), metadata.Name, "actions-runner-controller")
	assert.Equal(s.T(), metadata.Namespace, namespace)
	assert.NotNil(s.T(), metadata.Values)
	assert.Equal(s.T(), metadata.Version, "0.23.7")


	metadataRunners := map[string][]helm.Metadata{}

	atmos.OutputStruct(s.T(), options, "metadata_action_runner_releases", &metadataRunners)

	assert.Equal(s.T(), len(metadataRunners), 1)

	runnerMetadata := metadataRunners["infra-runner"][0]

	assert.Equal(s.T(), runnerMetadata.AppVersion, "v1alpha1")
	assert.Equal(s.T(), runnerMetadata.Chart, "actions-runner")
	assert.NotNil(s.T(), runnerMetadata.FirstDeployed)
	assert.NotNil(s.T(), runnerMetadata.LastDeployed)
	assert.Equal(s.T(), runnerMetadata.Name, "infra-runner")
	assert.Equal(s.T(), runnerMetadata.Namespace, namespace)
	assert.NotNil(s.T(), runnerMetadata.Values)
	assert.Equal(s.T(), runnerMetadata.Version, "0.3.2")

	client := github.NewClient(nil).WithAuthToken(token)

	runners, _, err := client.Actions.ListOrganizationRunners(context.Background(), githubOrg, nil)
	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), runners)
	assert.True(s.T(), len(runners.Runners) > 0, "Expected at least one self-hosted runner")

	found := false
	for _, runner := range runners.Runners {
		for _, label := range runner.Labels {
			if label.GetName() == randomID {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	assert.True(s.T(), found, "Expected to find a self-hosted runner with the label")

	s.DriftTest(component, stack, &inputs)
}

func (s *ComponentSuite) TestEnabledFlag() {
	const component = "eks/actions-runner-controller/disabled"
	const stack = "default-test"
	s.VerifyEnabledFlag(component, stack, nil)
}

func (s *ComponentSuite) SetupSuite() {
	s.TestSuite.InitConfig()
	s.TestSuite.Config.ComponentDestDir = "components/terraform/eks/actions-runner-controller"
	s.TestSuite.SetupSuite()
}

func TestRunSuite(t *testing.T) {
	suite := new(ComponentSuite)
	suite.AddDependency(t, "vpc", "default-test", nil)

	subdomain := strings.ToLower(random.UniqueId())
	inputs := map[string]interface{}{
		"zone_config": []map[string]interface{}{
			{
				"subdomain": subdomain,
				"zone_name": "components.cptest.test-automation.app",
			},
		},
	}
	suite.AddDependency(t, "dns-delegated", "default-test", &inputs)

	suite.AddDependency(t, "eks/cluster", "default-test", nil)
	suite.AddDependency(t, "eks/cert-manager", "default-test", nil)
	helper.Run(t, suite)
}

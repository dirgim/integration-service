/*
Copyright 2022 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package status

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	applicationapiv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/integration-service/git/github"
	"github.com/redhat-appstudio/integration-service/gitops"
	"github.com/redhat-appstudio/integration-service/helpers"
	"github.com/redhat-appstudio/operator-toolkit/metadata"
	tektonv1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GitHubReporter reports status back to GitHub for a PipelineRun.
type GitHubReporter struct {
	logger    logr.Logger
	k8sClient client.Client
	client    github.ClientInterface
}

// GitHubReporterOption is used to extend GitHubReporter with optional parameters.
type GitHubReporterOption = func(r *GitHubReporter)

func WithGitHubClient(client github.ClientInterface) GitHubReporterOption {
	return func(r *GitHubReporter) {
		r.client = client
	}
}

// NewGitHubReporter returns a struct implementing the Reporter interface for GitHub
func NewGitHubReporter(logger logr.Logger, k8sClient client.Client, opts ...GitHubReporterOption) *GitHubReporter {
	reporter := GitHubReporter{
		logger:    logger,
		k8sClient: k8sClient,
		client:    github.NewClient(logger),
	}

	for _, opt := range opts {
		opt(&reporter)
	}

	return &reporter
}

type appCredentials struct {
	AppID          int64
	InstallationID int64
	PrivateKey     []byte
}

func (r *GitHubReporter) getAppCredentials(ctx context.Context, object client.Object) (*appCredentials, error) {
	var err error
	var found bool
	appInfo := appCredentials{}

	appInfo.InstallationID, err = strconv.ParseInt(object.GetAnnotations()[gitops.PipelineAsCodeInstallationIDAnnotation], 10, 64)
	if err != nil {
		return nil, err
	}

	// Get the global pipelines as code secret
	pacSecret := v1.Secret{}
	err = r.k8sClient.Get(ctx, types.NamespacedName{Namespace: "openshift-pipelines", Name: "pipelines-as-code-secret"}, &pacSecret)
	if err != nil {
		return nil, err
	}

	// Get the App ID from the secret
	ghAppIDBytes, found := pacSecret.Data["github-application-id"]
	if !found {
		return nil, errors.New("failed to find github-application-id secret key")
	}

	appInfo.AppID, err = strconv.ParseInt(string(ghAppIDBytes), 10, 64)
	if err != nil {
		return nil, err
	}

	// Get the App's private key from the secret
	appInfo.PrivateKey, found = pacSecret.Data["github-private-key"]
	if !found {
		return nil, errors.New("failed to find github-private-key secret key")
	}

	return &appInfo, nil
}

func (r *GitHubReporter) getToken(ctx context.Context, object client.Object, namespace string) (string, error) {
	var err error

	// List all the Repository CRs in the namespace
	repos := pacv1alpha1.RepositoryList{}
	if err = r.k8sClient.List(ctx, &repos, &client.ListOptions{Namespace: namespace}); err != nil {
		return "", err
	}

	// Get the full repo URL
	url, found := object.GetAnnotations()[gitops.PipelineAsCodeRepoURLAnnotation]
	if !found {
		return "", fmt.Errorf("object annotation not found %q", gitops.PipelineAsCodeRepoURLAnnotation)
	}

	// Find a Repository CR with a matching URL and get its secret details
	var repoSecret *pacv1alpha1.Secret
	for _, repo := range repos.Items {
		if url == repo.Spec.URL {
			repoSecret = repo.Spec.GitProvider.Secret
			break
		}
	}

	if repoSecret == nil {
		return "", fmt.Errorf("failed to find a Repository matching URL: %q", url)
	}

	// Get the pipelines as code secret from the PipelineRun's namespace
	pacSecret := v1.Secret{}
	err = r.k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: repoSecret.Name}, &pacSecret)
	if err != nil {
		return "", err
	}

	// Get the personal access token from the secret
	token, found := pacSecret.Data[repoSecret.Key]
	if !found {
		return "", fmt.Errorf("failed to find %s secret key", repoSecret.Key)
	}

	return string(token), nil
}

func (r *GitHubReporter) createCheckRunAdapter(k8sClient client.Client, ctx context.Context, pipelineRun *tektonv1beta1.PipelineRun) (*github.CheckRunAdapter, error) {
	labels := pipelineRun.GetLabels()

	scenario, found := labels[gitops.SnapshotTestScenarioLabel]
	if !found {
		return nil, fmt.Errorf("PipelineRun label not found %q", gitops.SnapshotTestScenarioLabel)
	}

	component, found := labels[gitops.SnapshotComponentLabel]
	if !found {
		return nil, fmt.Errorf("PipelineRun label not found %q", gitops.SnapshotComponentLabel)
	}

	owner, found := labels[gitops.PipelineAsCodeURLOrgLabel]
	if !found {
		return nil, fmt.Errorf("PipelineRun label not found %q", gitops.PipelineAsCodeURLOrgLabel)
	}

	repo, found := labels[gitops.PipelineAsCodeURLRepositoryLabel]
	if !found {
		return nil, fmt.Errorf("PipelineRun label not found %q", gitops.PipelineAsCodeURLRepositoryLabel)
	}

	SHA, found := labels[gitops.PipelineAsCodeSHALabel]
	if !found {
		return nil, fmt.Errorf("PipelineRun label not found %q", gitops.PipelineAsCodeSHALabel)
	}

	var title, conclusion string
	succeeded := pipelineRun.Status.GetCondition(apis.ConditionSucceeded)

	if succeeded.IsUnknown() {
		title = scenario + " has started"
	} else {
		outcome, err := helpers.GetIntegrationPipelineRunOutcome(k8sClient, ctx, pipelineRun)

		if err != nil {
			return nil, err
		}

		if outcome.HasPipelineRunPassedTesting() {
			title = scenario + " has succeeded"
			conclusion = "success"
		} else {
			title = scenario + " has failed"
			conclusion = "failure"
		}
	}

	taskRuns, err := helpers.GetAllChildTaskRunsForPipelineRun(r.k8sClient, ctx, pipelineRun)
	if err != nil {
		return nil, fmt.Errorf("error while getting all child taskRuns from pipelineRun %s: %w", pipelineRun.Name, err)
	}
	summary, err := FormatSummary(taskRuns)
	if err != nil {
		return nil, err
	}

	startTime := time.Time{}
	if start := pipelineRun.Status.StartTime; start != nil {
		startTime = start.Time
	}

	completionTime := time.Time{}
	if complete := pipelineRun.Status.CompletionTime; complete != nil {
		completionTime = complete.Time
	}

	text := ""
	if !succeeded.IsUnknown() {
		text = succeeded.Message
	}

	return &github.CheckRunAdapter{
		Owner:          owner,
		Repository:     repo,
		Name:           NamePrefix + " / " + component + " / " + scenario,
		SHA:            SHA,
		ExternalID:     pipelineRun.Name,
		Conclusion:     conclusion,
		Title:          title,
		Summary:        summary,
		Text:           text,
		StartTime:      startTime,
		CompletionTime: completionTime,
	}, nil
}

// generateSummary generate a string for the given state, snapshotName and scenarioName
func generateSummary(state gitops.IntegrationTestStatus, snapshotName, scenarioName string) (string, error) {
	var title string

	var statusDesc string = "is unknown"

	switch state {
	case gitops.IntegrationTestStatusPending:
		statusDesc = "is pending"
	case gitops.IntegrationTestStatusInProgress:
		statusDesc = "is in progress"
	case gitops.IntegrationTestStatusEnvironmentProvisionError:
		statusDesc = "experienced an error when provisioning environment"
	case gitops.IntegrationTestStatusDeploymentError:
		statusDesc = "experienced an error when deploying snapshotEnvironmentBinding"
	case gitops.IntegrationTestStatusTestPassed:
		statusDesc = "has passed"
	case gitops.IntegrationTestStatusTestFail:
		statusDesc = "has failed"
	default:
		return title, fmt.Errorf("unknown status")
	}

	title = fmt.Sprintf("Integration test for snapshot %s and scenario %s %s", snapshotName, scenarioName, statusDesc)

	return title, nil
}

// generateCheckRunConclusion generate a conclusion as the conclusion of CheckRun
// can be Can be one of: action_required, cancelled, failure, neutral, success, skipped, stale, timed_out
// https://docs.github.com/en/rest/checks/runs?apiVersion=2022-11-28#create-a-check-run
func generateCheckRunConclusion(state gitops.IntegrationTestStatus) (string, error) {
	var conclusion string

	switch state {
	case gitops.IntegrationTestStatusTestFail, gitops.IntegrationTestStatusEnvironmentProvisionError, gitops.IntegrationTestStatusDeploymentError:
		conclusion = gitops.IntegrationTestStatusFailureGithub
	case gitops.IntegrationTestStatusTestPassed:
		conclusion = gitops.IntegrationTestStatusSuccessGithub
	case gitops.IntegrationTestStatusPending, gitops.IntegrationTestStatusInProgress:
		conclusion = ""
	default:
		return conclusion, fmt.Errorf("unknown status")
	}

	return conclusion, nil
}

// generateCommitState generate state of CommitStatus
// Can be one of: error, failure, pending, success
// https://docs.github.com/en/rest/commits/statuses?apiVersion=2022-11-28#create-a-commit-status
func generateCommitState(state gitops.IntegrationTestStatus) (string, error) {
	var commitState string

	switch state {
	case gitops.IntegrationTestStatusTestFail:
		commitState = gitops.IntegrationTestStatusFailureGithub
	case gitops.IntegrationTestStatusEnvironmentProvisionError, gitops.IntegrationTestStatusDeploymentError:
		commitState = gitops.IntegrationTestStatusErrorGithub
	case gitops.IntegrationTestStatusTestPassed:
		commitState = gitops.IntegrationTestStatusSuccessGithub
	case gitops.IntegrationTestStatusPending, gitops.IntegrationTestStatusInProgress:
		commitState = gitops.IntegrationTestStatusPendingGithub
	default:
		return commitState, fmt.Errorf("unknown status")
	}

	return commitState, nil
}

// createCheckRunAdapterForSnapshot create a CheckRunAdapter for given snapshot, integrationTestStatusDetail, owner, repo and sha to create a checkRun
// https://docs.github.com/en/rest/checks/runs?apiVersion=2022-11-28#create-a-check-run
func (r *GitHubReporter) createCheckRunAdapterForSnapshot(snapshot *applicationapiv1alpha1.Snapshot, integrationTestStatusDetail gitops.IntegrationTestStatusDetail, owner, repo, sha string) (*github.CheckRunAdapter, error) {
	snapshotName := snapshot.Name
	scenarioName := integrationTestStatusDetail.ScenarioName

	conclusion, err := generateCheckRunConclusion(integrationTestStatusDetail.Status)
	if err != nil {
		return nil, fmt.Errorf("unknown status %s for integrationTestScenario %s and snapshot %s/%s", integrationTestStatusDetail.Status, scenarioName, snapshot.Namespace, snapshot.Name)
	}

	summary, err := generateSummary(integrationTestStatusDetail.Status, snapshotName, scenarioName)
	if err != nil {
		return nil, fmt.Errorf("unknown status %s for integrationTestScenario %s and snapshot %s/%s", integrationTestStatusDetail.Status, scenarioName, snapshot.Namespace, snapshot.Name)
	}

	cra := &github.CheckRunAdapter{
		Owner:      owner,
		Repository: repo,
		Name:       NamePrefix + " / " + snapshotName + " / " + scenarioName,
		SHA:        sha,
		ExternalID: scenarioName,
		Conclusion: conclusion,
		Title:      conclusion,
		// This summary will be reworked once PLNSRVCE-1295 is implemented in the future
		Summary: summary,
		Text:    integrationTestStatusDetail.Details,
	}

	if start := integrationTestStatusDetail.StartTime; start != nil {
		cra.StartTime = *start
	}

	if complete := integrationTestStatusDetail.CompletionTime; complete != nil {
		cra.CompletionTime = *complete
	}

	return cra, nil
}

func (r *GitHubReporter) createCommitStatus(k8sClient client.Client, ctx context.Context, pipelineRun *tektonv1beta1.PipelineRun) error {
	var (
		state       string
		description string
	)

	labels := pipelineRun.GetLabels()

	scenario, found := labels[gitops.SnapshotTestScenarioLabel]
	if !found {
		return fmt.Errorf("PipelineRun label not found %q", gitops.SnapshotTestScenarioLabel)
	}

	component, found := labels[gitops.SnapshotComponentLabel]
	if !found {
		return fmt.Errorf("PipelineRun label not found %q", gitops.SnapshotComponentLabel)
	}

	owner, found := labels[gitops.PipelineAsCodeURLOrgLabel]
	if !found {
		return fmt.Errorf("PipelineRun label not found %q", gitops.PipelineAsCodeURLOrgLabel)
	}

	repo, found := labels[gitops.PipelineAsCodeURLRepositoryLabel]
	if !found {
		return fmt.Errorf("PipelineRun label not found %q", gitops.PipelineAsCodeURLRepositoryLabel)
	}

	SHA, found := labels[gitops.PipelineAsCodeSHALabel]
	if !found {
		return fmt.Errorf("PipelineRun label not found %q", gitops.PipelineAsCodeSHALabel)
	}

	statusContext := NamePrefix + " / " + component + " / " + scenario

	succeeded := pipelineRun.Status.GetCondition(apis.ConditionSucceeded)

	if succeeded.IsUnknown() {
		state = "pending"
		description = scenario + " has started"
	} else {
		outcome, err := helpers.GetIntegrationPipelineRunOutcome(k8sClient, ctx, pipelineRun)
		if err != nil {
			return err
		}

		if outcome.HasPipelineRunPassedTesting() {
			state = "success"
			description = scenario + " has succeeded"
		} else {
			state = "failure"
			description = scenario + " has failed"
		}
	}

	_, err := r.client.CreateCommitStatus(ctx, owner, repo, SHA, state, description, statusContext)
	if err != nil {
		return err
	}

	return nil
}

// createCommitStatusAdapterForSnapshot create a commitStatusAdapter used to create commitStatus on GitHub
// https://docs.github.com/en/rest/commits/statuses?apiVersion=2022-11-28#create-a-commit-status
func (r *GitHubReporter) createCommitStatusAdapterForSnapshot(snapshot *applicationapiv1alpha1.Snapshot, integrationTestStatusDetail gitops.IntegrationTestStatusDetail, owner, repo, sha string) (*github.CommitStatusAdapter, error) {
	snapshotName := snapshot.Name
	scenarioName := integrationTestStatusDetail.ScenarioName
	statusContext := NamePrefix + " / " + snapshot.Name + " / " + scenarioName

	state, err := generateCommitState(integrationTestStatusDetail.Status)
	if err != nil {
		return nil, fmt.Errorf("unknown status %s for integrationTestScenario %s and snapshot %s/%s", integrationTestStatusDetail.Status, scenarioName, snapshot.Namespace, snapshot.Name)
	}

	description, err := generateSummary(integrationTestStatusDetail.Status, snapshotName, scenarioName)
	if err != nil {
		return nil, fmt.Errorf("unknown status %s for integrationTestScenario %s and snapshot %s/%s", integrationTestStatusDetail.Status, scenarioName, snapshot.Namespace, snapshot.Name)
	}

	return &github.CommitStatusAdapter{
		Owner:       owner,
		Repository:  repo,
		SHA:         sha,
		State:       state,
		Description: description,
		Context:     statusContext,
	}, nil
}

func (r *GitHubReporter) createComment(k8sClient client.Client, ctx context.Context, pipelineRun *tektonv1beta1.PipelineRun) error {
	labels := pipelineRun.GetLabels()

	succeeded := pipelineRun.Status.GetCondition(apis.ConditionSucceeded)
	if succeeded.IsUnknown() {
		return nil
	}

	scenario, found := labels[gitops.SnapshotTestScenarioLabel]
	if !found {
		return fmt.Errorf("PipelineRun label not found %q", gitops.SnapshotTestScenarioLabel)
	}

	owner, found := labels[gitops.PipelineAsCodeURLOrgLabel]
	if !found {
		return fmt.Errorf("PipelineRun label not found %q", gitops.PipelineAsCodeURLOrgLabel)
	}

	repo, found := labels[gitops.PipelineAsCodeURLRepositoryLabel]
	if !found {
		return fmt.Errorf("PipelineRun label not found %q", gitops.PipelineAsCodeURLRepositoryLabel)
	}

	issueNumberStr, found := pipelineRun.GetAnnotations()[gitops.PipelineAsCodePullRequestAnnotation]
	if !found {
		return fmt.Errorf("PipelineRun label not found %q", gitops.PipelineAsCodeURLRepositoryLabel)
	}

	issueNumber, err := strconv.Atoi(issueNumberStr)
	if err != nil {
		return err
	}

	outcome, err := helpers.GetIntegrationPipelineRunOutcome(k8sClient, ctx, pipelineRun)
	if err != nil {
		return err
	}

	var title string
	if outcome.HasPipelineRunPassedTesting() {
		title = scenario + " has succeeded"
	} else {
		title = scenario + " has failed"
	}

	taskRuns, err := helpers.GetAllChildTaskRunsForPipelineRun(r.k8sClient, ctx, pipelineRun)
	if err != nil {
		return fmt.Errorf("error while getting all child taskRuns from pipelineRun %s: %w", pipelineRun.Name, err)
	}
	comment, err := FormatComment(title, taskRuns)
	if err != nil {
		return err
	}

	_, err = r.client.CreateComment(ctx, owner, repo, issueNumber, comment)
	if err != nil {
		return err
	}

	return nil
}

// ReportStatus creates/updates CheckRuns when using GitHub App integration.
// When using GitHub webhook integration a commit status and, in some cases, a comment is created.
func (r *GitHubReporter) ReportStatus(k8sClient client.Client, ctx context.Context, pipelineRun *tektonv1beta1.PipelineRun) error {
	if !metadata.HasLabelWithValue(pipelineRun, gitops.PipelineAsCodeEventTypeLabel, gitops.PipelineAsCodePullRequestType) {
		return nil
	}

	// Existence of the Pipelines as Code installation ID annotation signals configuration using GitHub App integration.
	// If it doesn't exist, GitHub webhook integration is configured.
	if metadata.HasAnnotation(pipelineRun, gitops.PipelineAsCodeInstallationIDAnnotation) {
		creds, err := r.getAppCredentials(ctx, pipelineRun)
		if err != nil {
			return err
		}

		token, err := r.client.CreateAppInstallationToken(ctx, creds.AppID, creds.InstallationID, creds.PrivateKey)
		if err != nil {
			return err
		}

		r.client.SetOAuthToken(ctx, token)

		checkRun, err := r.createCheckRunAdapter(k8sClient, ctx, pipelineRun)
		if err != nil {
			return err
		}

		checkRunID, err := r.client.GetCheckRunID(ctx, checkRun.Owner, checkRun.Repository, checkRun.SHA, checkRun.ExternalID, creds.AppID)
		if err != nil {
			return err
		}

		if checkRunID == nil {
			_, err = r.client.CreateCheckRun(ctx, checkRun)
		} else {
			err = r.client.UpdateCheckRun(ctx, *checkRunID, checkRun)
		}

		if err != nil {
			return err
		}
	} else {
		token, err := r.getToken(ctx, pipelineRun, pipelineRun.Namespace)
		if err != nil {
			return err
		}

		r.client.SetOAuthToken(ctx, token)

		err = r.createCommitStatus(k8sClient, ctx, pipelineRun)
		if err != nil {
			return err
		}

		err = r.createComment(k8sClient, ctx, pipelineRun)
		if err != nil {
			return err
		}
	}

	return nil
}

// ReportStatusForSnapshot creates CheckRun when using GitHub App integration,
// creates a a commit status when using GitHub webhook integration
func (r *GitHubReporter) ReportStatusForSnapshot(k8sClient client.Client, ctx context.Context, logger *helpers.IntegrationLogger, snapshot *applicationapiv1alpha1.Snapshot) error {
	statuses, err := gitops.NewSnapshotIntegrationTestStatusesFromSnapshot(snapshot)
	if err != nil {
		logger.Error(err, "failed to get test status annotations from snapshot",
			"snapshot.Namespace", snapshot.Namespace, "snapshot.Name", snapshot.Name)
		return err
	}

	labels := snapshot.GetLabels()

	owner, found := labels[gitops.PipelineAsCodeURLOrgLabel]
	if !found {
		return fmt.Errorf("org label not found %q", gitops.PipelineAsCodeURLOrgLabel)
	}

	repo, found := labels[gitops.PipelineAsCodeURLRepositoryLabel]
	if !found {
		return fmt.Errorf("repository label not found %q", gitops.PipelineAsCodeURLRepositoryLabel)
	}

	sha, found := labels[gitops.PipelineAsCodeSHALabel]
	if !found {
		return fmt.Errorf("sha label not found %q", gitops.PipelineAsCodeSHALabel)
	}
	integrationTestStatusDetails := statuses.GetStatuses()
	// Existence of the Pipelines as Code installation ID annotation signals configuration using GitHub App integration.
	// If it doesn't exist, GitHub webhook integration is configured.
	if metadata.HasAnnotation(snapshot, gitops.PipelineAsCodeInstallationIDAnnotation) {
		creds, err := r.getAppCredentials(ctx, snapshot)
		if err != nil {
			logger.Error(err, "failed to get app credentials from Snapshot",
				"snapshot.NameSpace", snapshot.Namespace, "snapshot.Name", snapshot.Name)
			return err
		}

		token, err := r.client.CreateAppInstallationToken(ctx, creds.AppID, creds.InstallationID, creds.PrivateKey)
		if err != nil {
			logger.Error(err, "failed to create app installation token",
				"creds.AppID", creds.AppID, "creds.InstallationID", creds.InstallationID)
			return err
		}

		r.client.SetOAuthToken(ctx, token)

		allCheckRuns, err := r.client.GetAllCheckRunsForRef(ctx, owner, repo, sha, creds.AppID)
		if err != nil {
			logger.Error(err, "failed to get all checkruns for ref",
				"owner", owner, "repo", repo, "creds.AppID", creds.AppID)
			return err
		}

		for _, integrationTestStatusDetail := range integrationTestStatusDetails {
			integrationTestStatusDetail := *integrationTestStatusDetail // G601
			checkRun, err := r.createCheckRunAdapterForSnapshot(snapshot, integrationTestStatusDetail, owner, repo, sha)
			if err != nil {
				logger.Error(err, "failed to create checkRunAdapter for snapshot",
					"snapshot.NameSpace", snapshot.Namespace, "snapshot.Name", snapshot.Name)
				return err
			}

			existingCheckrun := r.client.GetExistingCheckRun(allCheckRuns, checkRun)

			if existingCheckrun == nil {
				logger.Info("creating checkrun for scenario test status of snapshot",
					"snapshot.NameSpace", snapshot.Namespace, "snapshot.Name", snapshot.Name, "scenarioName", integrationTestStatusDetail.ScenarioName)
				_, err = r.client.CreateCheckRun(ctx, checkRun)
				if err != nil {
					logger.Error(err, "failed to create checkrun",
						"checkRun", checkRun)
					return err
				}
			} else {
				logger.Info("found existing checkrun", "existingCheckRun", existingCheckrun)
				if r.client.IsUpdateNeeded(existingCheckrun, checkRun) {
					logger.Info("found existing check run with the same ExternalID but different conclusion/status, updating checkrun for scenario test status of snapshot",
						"snapshot.NameSpace", snapshot.Namespace, "snapshot.Name", snapshot.Name, "scenarioName", integrationTestStatusDetail.ScenarioName, "checkrun.ExternalID", checkRun.ExternalID)
					err = r.client.UpdateCheckRun(ctx, *existingCheckrun.ID, checkRun)
					if err != nil {
						logger.Error(err, "failed to update checkrun",
							"checkRun", checkRun)
						return err
					}
				} else {
					logger.Info("found existing check run with the same ExternalID and conclusion/status, no need to update checkrun for scenario test status of snapshot",
						"snapshot.NameSpace", snapshot.Namespace, "snapshot.Name", snapshot.Name, "scenarioName", integrationTestStatusDetail.ScenarioName, "checkrun.ExternalID", checkRun.ExternalID)
				}
			}
		}
	} else {
		token, err := r.getToken(ctx, snapshot, snapshot.Namespace)
		if err != nil {
			logger.Error(err, "failed to get token from snapshot",
				"snapshot.NameSpace", snapshot.Namespace, "snapshot.Name", snapshot.Name)
			return err
		}

		r.client.SetOAuthToken(ctx, token)

		allCommitStatuses, err := r.client.GetAllCommitStatusesForRef(ctx, owner, repo, sha)
		if err != nil {
			logger.Error(err, "failed to get all commitStatuses for snapshot",
				"snapshot.NameSpace", snapshot.Namespace, "snapshot.Name", snapshot.Name)
			return err
		}

		for _, integrationTestStatusDetail := range integrationTestStatusDetails {
			integrationTestStatusDetail := *integrationTestStatusDetail //G601
			commitStatus, err := r.createCommitStatusAdapterForSnapshot(snapshot, integrationTestStatusDetail, owner, repo, sha)
			if err != nil {
				logger.Error(err, "failed to create CommitStatusAdapter for snapshot",
					"snapshot.NameSpace", snapshot.Namespace, "snapshot.Name", snapshot.Name)
				return err
			}

			commitStatusExist, err := r.client.CommitStatusExists(allCommitStatuses, commitStatus)
			if err != nil {
				return err
			}

			if !commitStatusExist {
				logger.Info("creating commit status for scenario test status of snapshot",
					"snapshot.NameSpace", snapshot.Namespace, "snapshot.Name", snapshot.Name, "scenarioName", integrationTestStatusDetail.ScenarioName)
				_, err = r.client.CreateCommitStatus(ctx, commitStatus.Owner, commitStatus.Repository, commitStatus.SHA, commitStatus.State, commitStatus.Description, commitStatus.Context)
				if err != nil {
					return err
				}
			} else {
				logger.Info("found existing commitStatus for scenario test status of snapshot, no need to create new commit status",
					"snapshot.NameSpace", snapshot.Namespace, "snapshot.Name", snapshot.Name, "scenarioName", integrationTestStatusDetail.ScenarioName)
			}

		}
	}

	return nil
}

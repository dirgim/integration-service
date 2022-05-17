/*
Copyright 2022.

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

package tekton

import (
	"fmt"
	"github.com/redhat-appstudio/integration-service/api/v1alpha1"
	releasev1alpha1 "github.com/redhat-appstudio/release-service/api/v1alpha1"
	tektonv1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PipelineType represents a PipelineRun type within AppStudio
type PipelineType string

const (
	// pipelinesLabelPrefix is the prefix of the pipelines label
	pipelinesLabelPrefix = "pipelines.appstudio.openshift.io"

	// integrationLabelPrefix is the prefix of the integration labels
	integrationLabelPrefix = "integration.appstudio.openshift.io"

	// applicationsnapshotLabelPrefix is the prefix of the applicationSnapshot labels
	applicationsnapshotLabelPrefix = "applicationsnapshot.appstudio.openshift.io"

	//PipelineTypePreliminary is the type for PipelineRuns created to run a preliminary integration Pipeline
	PipelineTypePreliminary = "preliminary"

	//PipelineTypeFinal is the type for PipelineRuns created to run a final integration Pipeline
	PipelineTypeFinal = "final"
)

var (
	// PipelinesTypeLabel is the label used to describe the type of pipeline
	PipelinesTypeLabel = fmt.Sprintf("%s/%s", pipelinesLabelPrefix, "type")

	// IntegrationNameLabel is the label used to specify the name of the Integration associated with the PipelineRun
	IntegrationNameLabel = fmt.Sprintf("%s/%s", integrationLabelPrefix, "name")

	// ApplicationSnapshotNameLabel is the label used to specify the name of the Integration associated with the PipelineRun
	ApplicationSnapshotNameLabel = fmt.Sprintf("%s/%s", applicationsnapshotLabelPrefix, "name")

	// IntegrationWorkspaceLabel is the label used to specify the workspace of the Integration associated with the PipelineRun
	IntegrationWorkspaceLabel = fmt.Sprintf("%s/%s", integrationLabelPrefix, "workspace")
)

// CreatePreliminaryPipelineRun creates a PipelineRun from a given ApplicationSnapshot and IntegrationScenario.
// ApplicationSnapshot details are added to the labels of the new PipelineRun to be able to reference it later on.
func CreatePreliminaryPipelineRun(applicationSnapshot *releasev1alpha1.ApplicationSnapshot, integrationScenario *v1alpha1.IntegrationScenario) *tektonv1beta1.PipelineRun {
	return &tektonv1beta1.PipelineRun{
		ObjectMeta: v1.ObjectMeta{
			GenerateName: integrationScenario.Name + "-",
			Labels: map[string]string{
				PipelinesTypeLabel:           PipelineTypePreliminary,
				IntegrationNameLabel:         applicationSnapshot.Name,
				ApplicationSnapshotNameLabel: applicationSnapshot.Name,
				IntegrationWorkspaceLabel:    applicationSnapshot.Namespace,
			},
			Namespace: integrationScenario.Namespace,
		},
		Spec: tektonv1beta1.PipelineRunSpec{
			PipelineRef: &tektonv1beta1.PipelineRef{
				Name:   integrationScenario.Spec.Pipeline,
				Bundle: integrationScenario.Spec.Bundle,
			},
		},
	}
}

// CreatePreliminaryPipelineRun creates a PipelineRun from a given ApplicationSnapshot and IntegrationScenario.
// ApplicationSnapshot details are added to the labels of the new PipelineRun to be able to reference it later on.
func CreateFinalPipelineRun(applicationSnapshot *releasev1alpha1.ApplicationSnapshot, integrationScenario *v1alpha1.IntegrationScenario) *tektonv1beta1.PipelineRun {
	return &tektonv1beta1.PipelineRun{
		ObjectMeta: v1.ObjectMeta{
			GenerateName: integrationScenario.Name + "-",
			Labels: map[string]string{
				PipelinesTypeLabel:           PipelineTypeFinal,
				IntegrationNameLabel:         integrationScenario.Name,
				ApplicationSnapshotNameLabel: applicationSnapshot.Name,
				IntegrationWorkspaceLabel:    integrationScenario.Namespace,
			},
			Namespace: integrationScenario.Namespace,
		},
		Spec: tektonv1beta1.PipelineRunSpec{
			PipelineRef: &tektonv1beta1.PipelineRef{
				Name:   integrationScenario.Spec.Pipeline,
				Bundle: integrationScenario.Spec.Bundle,
			},
		},
	}
}

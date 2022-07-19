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
	appstudioshared "github.com/dirgim/managed-gitops/appstudio-shared/apis/appstudio.redhat.com/v1alpha1"
	hasv1alpha1 "github.com/redhat-appstudio/application-service/api/v1alpha1"
	"github.com/redhat-appstudio/integration-service/api/v1alpha1"
	tektonv1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PipelineType represents a PipelineRun type within AppStudio
type PipelineType string

const (
	// pipelineLabelPrefix is the prefix of the pipeline labels
	pipelineLabelPrefix = "pipelines.appstudio.openshift.io"
	// testLabelPrefix is the prefix of the test integration labels
	testLabelPrefix = "test.appstudio.openshift.io"

	//PipelineTypeComponent is the type for PipelineRuns created to run a preliminary integration Pipeline
	PipelineTypeComponent = "component"

	//PipelineTypeComposite is the type for PipelineRuns created to run a final integration Pipeline
	PipelineTypeComposite = "composite"
)

var (
	// PipelinesTypeLabel is the label used to describe the type of pipeline
	PipelinesTypeLabel = fmt.Sprintf("%s/%s", pipelineLabelPrefix, "type")

	// TestTypeLabel is the label used to specify the test type for the pipeline
	TestTypeLabel = fmt.Sprintf("%s/%s", testLabelPrefix, "test")

	// ApplicationSnapshotLabel is the label used to specify the name of the ApplicationSnapshot associated with the PipelineRun
	ApplicationSnapshotLabel = fmt.Sprintf("%s/%s", testLabelPrefix, "snapshot")

	// ComponentLabel is the label used to specify the name of the Component associated with the PipelineRun
	ComponentLabel = fmt.Sprintf("%s/%s", testLabelPrefix, "component")

	// ApplicationLabel is the label used to specify the Application associated with the PipelineRun
	ApplicationLabel = fmt.Sprintf("%s/%s", testLabelPrefix, "application")

	// IntegrationScenarioLabel is the label used to specify the IntegrationScenario associated with the PipelineRun
	IntegrationScenarioLabel = fmt.Sprintf("%s/%s", testLabelPrefix, "scenario")
)

// CreateComponentPipelineRun creates a PipelineRun from a given ApplicationSnapshot and IntegrationScenario.
// ApplicationSnapshot details are added to the labels of the new PipelineRun to be able to reference it later on.
func CreateComponentPipelineRun(component *hasv1alpha1.Component, application *hasv1alpha1.Application, applicationSnapshot *appstudioshared.ApplicationSnapshot, integrationTestScenario *v1alpha1.IntegrationTestScenario) *tektonv1beta1.PipelineRun {
	return &tektonv1beta1.PipelineRun{
		ObjectMeta: v1.ObjectMeta{
			GenerateName: integrationTestScenario.Name + "-",
			Labels: map[string]string{
				PipelinesTypeLabel:       "test",
				TestTypeLabel:            PipelineTypeComponent,
				ComponentLabel:           component.Name,
				ApplicationSnapshotLabel: applicationSnapshot.Name,
				ApplicationLabel:         application.Name,
				IntegrationScenarioLabel: integrationTestScenario.Name,
			},
			Namespace: component.Namespace,
		},
		Spec: tektonv1beta1.PipelineRunSpec{
			PipelineRef: &tektonv1beta1.PipelineRef{
				Name:   integrationTestScenario.Spec.Pipeline,
				Bundle: integrationTestScenario.Spec.Bundle,
			},
		},
	}
}

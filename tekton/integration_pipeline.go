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
	"encoding/json"
	"fmt"

	applicationapiv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/integration-service/api/v1alpha1"
	"github.com/redhat-appstudio/integration-service/helpers"
	tektonv1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// pipelinesLabelPrefix is the prefix of the pipelines label
	pipelinesLabelPrefix = "pipelines.appstudio.openshift.io"

	// testLabelPrefix is the prefix of the test labels
	testLabelPrefix = "test.appstudio.openshift.io"

	// PipelineTypeTest is the type for PipelineRuns created to run an integration Pipeline
	pipelineTypeTest = "test"
)

var (
	// PipelinesTypeLabel is the label used to describe the type of pipeline
	PipelinesTypeLabel = fmt.Sprintf("%s/%s", pipelinesLabelPrefix, "type")

	// TestNameLabel is the label used to specify the name of the Test associated with the PipelineRun
	TestNameLabel = fmt.Sprintf("%s/%s", testLabelPrefix, "name")

	// ScenarioNameLabel is the label used to specify the name of the IntegrationTestScenario associated with the PipelineRun
	ScenarioNameLabel = fmt.Sprintf("%s/%s", testLabelPrefix, "scenario")

	// SnapshotNameLabel is the label of specific the name of the snapshot associated with PipelineRun
	SnapshotNameLabel = fmt.Sprintf("%s/%s", testLabelPrefix, "snapshot")

	// ApplicationNameLabel is the label of specific the name of the Application associated with PipelineRun
	ApplicationNameLabel = fmt.Sprintf("%s/%s", testLabelPrefix, "application")

	// ComponentNameLabel is the label of specific the name of the component associated with PipelineRun
	ComponentNameLabel = fmt.Sprintf("%s/%s", testLabelPrefix, "component")

	// OptionalLabel is the label used to specify if an IntegrationTestScenario is allowed to fail
	OptionalLabel = fmt.Sprintf("%s/%s", testLabelPrefix, "optional")
)

// IntegrationPipelineRun is a PipelineRun alias, so we can add new methods to it in this file.
type IntegrationPipelineRun struct {
	tektonv1beta1.PipelineRun
}

// AsPipelineRun casts the IntegrationPipelineRun to PipelineRun, so it can be used in the Kubernetes client.
func (r *IntegrationPipelineRun) AsPipelineRun() *tektonv1beta1.PipelineRun {
	return &r.PipelineRun
}

// NewIntegrationPipelineRun creates an empty PipelineRun in the given namespace. The name will be autogenerated,
// using the prefix passed as an argument to the function.
func NewIntegrationPipelineRun(prefix, namespace string, integrationTestScenario v1alpha1.IntegrationTestScenario) *IntegrationPipelineRun {
	pipelineRun := tektonv1beta1.PipelineRun{
		ObjectMeta: v1.ObjectMeta{
			GenerateName: prefix + "-",
			Namespace:    namespace,
		},
		Spec: tektonv1beta1.PipelineRunSpec{
			PipelineRef: &tektonv1beta1.PipelineRef{
				Name:   integrationTestScenario.Spec.Pipeline,
				Bundle: integrationTestScenario.Spec.Bundle,
			},
		},
	}
	return &IntegrationPipelineRun{pipelineRun}
}

// WithExtraParam adds an extra param to the Integration PipelineRun. If the parameter is not part of the Pipeline
// definition, it will be silently ignored.
func (r *IntegrationPipelineRun) WithExtraParam(name string, value tektonv1beta1.ArrayOrString) *IntegrationPipelineRun {
	r.Spec.Params = append(r.Spec.Params, tektonv1beta1.Param{
		Name:  name,
		Value: value,
	})

	return r
}

// WithSnapshot adds a param containing the Snapshot as a json string
// to the integration PipelineRun.
func (r *IntegrationPipelineRun) WithSnapshot(snapshot *applicationapiv1alpha1.Snapshot) *IntegrationPipelineRun {
	// We ignore the error here because none should be raised when marshalling the spec of a CRD.
	// If we end up deciding it is useful, we will need to pass the errors trough the chain and
	// add something like a `Complete` function that returns the final object and error.
	snapshotString, _ := json.Marshal(snapshot.Spec)

	r.WithExtraParam("SNAPSHOT", tektonv1beta1.ArrayOrString{
		Type:      tektonv1beta1.ParamTypeString,
		StringVal: string(snapshotString),
	})

	if r.ObjectMeta.Labels == nil {
		r.ObjectMeta.Labels = map[string]string{}
	}
	r.ObjectMeta.Labels[SnapshotNameLabel] = snapshot.Name

	return r
}

// WithIntegrationLabels adds the type, optional flag and IntegrationTestScenario name as labels to the Integration PipelineRun.
func (r *IntegrationPipelineRun) WithIntegrationLabels(integrationTestScenario *v1alpha1.IntegrationTestScenario) *IntegrationPipelineRun {
	if r.ObjectMeta.Labels == nil {
		r.ObjectMeta.Labels = map[string]string{}
	}
	r.ObjectMeta.Labels[PipelinesTypeLabel] = pipelineTypeTest
	r.ObjectMeta.Labels[ScenarioNameLabel] = integrationTestScenario.Name

	if helpers.HasLabel(integrationTestScenario, OptionalLabel) {
		r.ObjectMeta.Labels[OptionalLabel] = integrationTestScenario.Labels[OptionalLabel]
	}

	return r
}

// WithApplicationAndComponent adds the name of both application and component as lables to the Integration PipelineRun.
func (r *IntegrationPipelineRun) WithApplicationAndComponent(application *applicationapiv1alpha1.Application, component *applicationapiv1alpha1.Component) *IntegrationPipelineRun {
	if r.ObjectMeta.Labels == nil {
		r.ObjectMeta.Labels = map[string]string{}
	}
	if component != nil {
		r.ObjectMeta.Labels[ComponentNameLabel] = component.Name
	}
	r.ObjectMeta.Labels[ApplicationNameLabel] = application.Name

	return r
}

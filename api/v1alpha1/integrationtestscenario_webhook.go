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

package v1alpha1

import (
	"context"
	"fmt"
	applicationapiv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

var (
	managerClient client.Client
)

func (in *IntegrationTestScenario) SetupWebhookWithManager(mgr ctrl.Manager) error {
	managerClient = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		For(in).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-appstudio-redhat-com-v1alpha1-integrationtestscenario,mutating=true,failurePolicy=fail,sideEffects=None,groups=appstudio.redhat.com,resources=integrationtestscenario,verbs=create,versions=v1alpha1,name=mintegrationtestscenario.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &IntegrationTestScenario{}

// Default implements webhook.Defaulter so a webhook will be registered for the type.
func (in *IntegrationTestScenario) Default() {

	if _, found := in.GetLabels()[OptionalScenarioLabel]; !found {
		if in.Labels == nil {
			in.Labels = map[string]string{}
		}
		in.Labels[OptionalScenarioLabel] = "false"
	}
	if in.OwnerReferences == nil {
		application := &applicationapiv1alpha1.Application{}
		err := managerClient.Get(context.TODO(), types.NamespacedName{
			Namespace: in.Namespace,
			Name:      in.Spec.Application,
		}, application)

		if err != nil {
			return
		}
		// The code below sets the Application ownership for the IntegrationTestScenario Object
		kind := reflect.TypeOf(applicationapiv1alpha1.Application{}).Name()
		gvk := GroupVersion.WithKind(kind)
		controllerRef := metav1.NewControllerRef(application, gvk)

		in.SetOwnerReferences([]metav1.OwnerReference{*controllerRef})
	}
}

// +kubebuilder:webhook:path=/validate-appstudio-redhat-com-v1alpha1-integrationtestscenario,mutating=false,failurePolicy=fail,sideEffects=None,groups=appstudio.redhat.com,resources=integrationtestscenarios,verbs=create;update,versions=v1alpha1,name=vintegrationtestscenario.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &IntegrationTestScenario{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (in *IntegrationTestScenario) ValidateCreate() error {
	return in.validateOptionalScenarioLabel()
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (in *IntegrationTestScenario) ValidateUpdate(old runtime.Object) error {
	return in.validateOptionalScenarioLabel()
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (in *IntegrationTestScenario) ValidateDelete() error {
	return nil
}

// validateOptionalScenarioLabel throws an error if the optional label value is set to anything besides true or false.
func (in *IntegrationTestScenario) validateOptionalScenarioLabel() error {
	if value, found := in.GetLabels()[OptionalScenarioLabel]; found {
		if value != "true" && value != "false" {
			return fmt.Errorf("'%s' label can only be set to true or false", OptionalScenarioLabel)
		}
	}
	return nil
}

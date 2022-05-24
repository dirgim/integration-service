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

package controllers

import (
	"context"
	"fmt"
	hasv1alpha1 "github.com/redhat-appstudio/application-service/api/v1alpha1"
	"github.com/redhat-appstudio/integration-service/api/v1alpha1"
	"github.com/redhat-appstudio/integration-service/tekton"
	releasev1alpha1 "github.com/redhat-appstudio/release-service/api/v1alpha1"
	"github.com/sirupsen/logrus"
	tektonv1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// IntegrationReconciler reconciles a Integration object
type IntegrationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=integrations,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=integrations/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=integrations/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.0/pkg/reconcile
func (r *IntegrationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logrus.New()

	pipelineRun := &tektonv1beta1.PipelineRun{}
	err := r.Get(ctx, req.NamespacedName, pipelineRun)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("PipelineRun resource not found")

			return ctrl.Result{}, nil
		}

		log.Error(err, " Failed to get PipelineRun")
		return ctrl.Result{}, err
	}

	if tekton.IsBuildPipelineRun(pipelineRun) {
		component, err := r.getComponentFromBuildPipelineRun(ctx, pipelineRun)
		if err != nil {
			log.Error(err, " Failed to get Component for ",
				"PipelineRun.Name ", pipelineRun.Name, "PipelineRun.Namespace ", pipelineRun.Namespace)
			return ctrl.Result{}, nil
		}

		application, err := r.getApplication(ctx, component)
		if err != nil {
			log.Error(err, " Failed to get Application for ",
				"Component.Name ", component.Name, "Component.Namespace ", component.Namespace)
			return ctrl.Result{}, nil
		}

		applicationComponents, err := r.getApplicationComponents(ctx, application)
		if err != nil {
			log.Error(err, " Failed to get Application Components for ",
				"Application.Name ", application.Name, "Application.Namespace ", application.Namespace)
			return ctrl.Result{}, nil
		}

		log.Info("PipelineRun resource for the build pipeline found! Component ", component.Name,
			" Application ", application.Name)
		applicationSnapshot, err := CreateApplicationSnapshot(component, applicationComponents, pipelineRun)
		err = r.Client.Create(ctx, applicationSnapshot)
		if err != nil {
			log.Error(err, " Failed to create Application Snapshot for ",
				"Application.Name ", application.Name, " Application.Namespace ", application.Namespace)
			return ctrl.Result{}, nil
		}
		log.Info("Created ApplicationSnapshot for Application ", application.Name,
			" with Images: ", applicationSnapshot.Spec.Images)

		prelimIntegrationScenarios, err := r.getIntegrationScenariosForTestContext(ctx, application, "preliminary")
		if err != nil {
			log.Error(err, " Failed to get IntegrationScenarios for ",
				"ApplicationSnapshot.Name ", applicationSnapshot.Name, " ApplicationSnapshot.Namespace ",
				applicationSnapshot.Namespace)
			return ctrl.Result{}, nil
		}
		for _, integrationScenario := range prelimIntegrationScenarios {
			pipelineRun := tekton.CreatePreliminaryPipelineRun(component, application, applicationSnapshot, &integrationScenario)
			err = r.Client.Create(ctx, pipelineRun)
			if err != nil {
				log.Error(err, " Failed to create Preliminary PipelineRun for ",
					"ApplicationSnapshot.Name ", applicationSnapshot.Name, " ApplicationSnapshot.Namespace ",
					applicationSnapshot.Namespace)
				return ctrl.Result{}, nil
			}
			log.Info("Created the Preliminary Tekton PipelineRun ", pipelineRun.Name, " using bundle ",
				pipelineRun.Spec.PipelineRef.Bundle, " with IntegrationScenario ", integrationScenario.Name, " for Application ", application.Name)
		}
	} else if tekton.IsPrelimPipelineRun(pipelineRun) {
		applicationSnapshot, err := r.getApplicationSnapshotFromTestPipelineRun(ctx, pipelineRun)
		if err != nil {
			log.Error(err, " Failed to get ApplicationSnapshot for ",
				"PipelineRun.Name ", pipelineRun.Name, "PipelineRun.Namespace ", pipelineRun.Namespace)
			return ctrl.Result{}, nil
		}
		log.Info("The Test PipelineRun ", pipelineRun.Name, " for the ApplicationSnapshot ",
			applicationSnapshot.Name, " with IntegrationScenario ",
			pipelineRun.Labels["test.appstudio.openshift.io/integrationscenario"], " Succeeded! ")
		allPipelineRuns, err := r.getPipelineRunsForApplicationSnapshot(ctx, applicationSnapshot)
		var allPipelineRunsSucceeded = true
		for _, snapshotPipelineRun := range allPipelineRuns {
			for _, condition := range snapshotPipelineRun.Status.Conditions {
				if condition.Type == "Succeeded" && condition.Status != "True" {
					allPipelineRunsSucceeded = false
				}
			}
		}
		if allPipelineRunsSucceeded {
			log.Info("All Test PipelineRuns for ApplicationSnapshot ", applicationSnapshot.Name, " have Succeeded!")
		}
	}

	return ctrl.Result{}, nil
}

// getComponentFromBuildPipelineRun loads from the cluster the Component referenced in the given build PipelineRun.
// If the PipelineRun doesn't specify a Component or this is not found in the cluster, an error will be returned.
func (r *IntegrationReconciler) getComponentFromBuildPipelineRun(ctx context.Context, pipelineRun *tektonv1beta1.PipelineRun) (*hasv1alpha1.Component, error) {
	if componentName, found := pipelineRun.Labels["build.appstudio.openshift.io/component"]; found {
		component := &hasv1alpha1.Component{}
		err := r.Get(ctx, types.NamespacedName{
			Namespace: pipelineRun.Namespace,
			Name:      componentName,
		}, component)

		if err != nil {
			return nil, err
		}

		return component, nil
	}

	return nil, fmt.Errorf("The pipeline has no component associated with it")
}

// getApplicationSnapshotFromTestPipelineRun loads from the cluster the Component referenced in the given PipelineRun. If the PipelineRun doesn't
// specify a Component or this is not found in the cluster, an error will be returned.
func (r *IntegrationReconciler) getApplicationSnapshotFromTestPipelineRun(ctx context.Context, pipelineRun *tektonv1beta1.PipelineRun) (*releasev1alpha1.ApplicationSnapshot, error) {
	if applicationSnapshotName, found := pipelineRun.Labels["test.appstudio.openshift.io/applicationsnapshot"]; found {
		applicationSnapshot := &releasev1alpha1.ApplicationSnapshot{}
		err := r.Get(ctx, types.NamespacedName{
			Namespace: pipelineRun.Namespace,
			Name:      applicationSnapshotName,
		}, applicationSnapshot)

		if err != nil {
			return nil, err
		}

		return applicationSnapshot, nil
	}

	return nil, fmt.Errorf("The pipeline has no component associated with it")
}

// getApplication loads from the cluster the Application referenced in the given Component. If the Component doesn't
// specify an Application or this is not found in the cluster, an error will be returned.
func (r *IntegrationReconciler) getApplication(ctx context.Context, component *hasv1alpha1.Component) (*hasv1alpha1.Application, error) {
	application := &hasv1alpha1.Application{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: component.Namespace,
		Name:      component.Spec.Application,
	}, application)

	if err != nil {
		return nil, err
	}

	return application, nil
}

// getApplicationComponents loads from the cluster the Components associated with the given Application. If the Application
// doesn't have any Components or this is not found in the cluster, an error will be returned.
func (r *IntegrationReconciler) getApplicationComponents(ctx context.Context, application *hasv1alpha1.Application) ([]hasv1alpha1.Component, error) {
	applicationComponents := []hasv1alpha1.Component{}
	allComponents := &hasv1alpha1.ComponentList{}
	err := r.List(ctx, allComponents)
	for _, component := range allComponents.Items {
		if component.Spec.Application == application.Name {
			applicationComponents = append(applicationComponents, component)
		}
	}

	if err != nil {
		return nil, err
	}

	return applicationComponents, nil
}

// getPipelineRunsForApplicationSnapshot loads from the cluster the pipelineRuns associated with the given ApplicationSnapshot.
//If there are no Pipeline runs for the ApplicationSnapshot or this is not found in the cluster, an error will be returned.
func (r *IntegrationReconciler) getPipelineRunsForApplicationSnapshot(ctx context.Context, applicationSnapshot *releasev1alpha1.ApplicationSnapshot) ([]tektonv1beta1.PipelineRun, error) {
	var pipelineRuns []tektonv1beta1.PipelineRun
	allPipelineRuns := &tektonv1beta1.PipelineRunList{}
	err := r.List(ctx, allPipelineRuns)
	for _, pipelineRun := range allPipelineRuns.Items {
		if pipelineRun.Labels["test.appstudio.openshift.io/applicationsnapshot"] == applicationSnapshot.Name {
			pipelineRuns = append(pipelineRuns, pipelineRun)
		}
	}

	if err != nil {
		return nil, err
	}

	return pipelineRuns, nil
}

// getIntegrationScenariosForTestContext get an IntegrationScenario for a given ApplicationSnapshot.
func (r *IntegrationReconciler) getIntegrationScenariosForTestContext(ctx context.Context, application *hasv1alpha1.Application, testContext string) ([]v1alpha1.IntegrationScenario, error) {
	var integrationScenarios []v1alpha1.IntegrationScenario
	allIntegrationScenarios := &v1alpha1.IntegrationScenarioList{}
	err := r.List(ctx, allIntegrationScenarios)
	for _, integrationScenario := range allIntegrationScenarios.Items {
		if integrationScenario.Spec.Application == application.Name {
			for _, integrationScenarioContext := range integrationScenario.Spec.Contexts {
				if integrationScenarioContext.Name == testContext {
					integrationScenarios = append(integrationScenarios, integrationScenario)
					continue
				}
			}
		}
	}

	if err != nil {
		return nil, err
	}

	return integrationScenarios, nil
}

func CreateApplicationSnapshot(component *hasv1alpha1.Component, applicationComponents []hasv1alpha1.Component, pipelineRun *tektonv1beta1.PipelineRun) (*releasev1alpha1.ApplicationSnapshot, error) {
	images := []releasev1alpha1.Image{}

	for _, applicationComponent := range applicationComponents {
		pullSpec := applicationComponent.Status.ContainerImage
		if applicationComponent.Name == component.Name {
			var err error
			pullSpec, err = tekton.GetOutputImage(pipelineRun)
			if err != nil {
				return nil, err
			}
		}
		image := releasev1alpha1.Image{
			Component: applicationComponent.Name,
			PullSpec:  pullSpec,
		}
		images = append(images, image)
	}
	return &releasev1alpha1.ApplicationSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: component.Spec.Application + "-",
			Namespace:    component.Namespace,
		},
		Spec: releasev1alpha1.ApplicationSnapshotSpec{
			Images: images,
		},
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *IntegrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&tektonv1beta1.PipelineRun{}).
		WithEventFilter(tekton.BuildOrPrelimPipelineRunSucceededPredicate()).
		Complete(r)
}

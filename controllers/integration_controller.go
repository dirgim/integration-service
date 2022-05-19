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

		integrationScenario, err := r.getIntegrationScenario(ctx, application)
		if err != nil {
			log.Error(err, " Failed to get IntegrationScenario for ",
				"ApplicationSnapshot.Name ", applicationSnapshot.Name, " ApplicationSnapshot.Namespace ",
				applicationSnapshot.Namespace)
			return ctrl.Result{}, nil
		}
		pipelineRun := tekton.CreatePreliminaryPipelineRun(component, application, applicationSnapshot, integrationScenario)
		err = r.Client.Create(ctx, pipelineRun)
		if err != nil {
			log.Error(err, " Failed to create Preliminary PipelineRun for ",
				"ApplicationSnapshot.Name ", applicationSnapshot.Name, " ApplicationSnapshot.Namespace ",
				applicationSnapshot.Namespace)
			return ctrl.Result{}, nil
		}
		log.Info("Created the Preliminary Tekton PipelineRun ", pipelineRun.Name, " using bundle ",
			pipelineRun.Spec.PipelineRef.Bundle, " with IntegrationScenario ", integrationScenario.Name, " for Application ", application.Name)
	} else if tekton.IsPrelimPipelineRun(pipelineRun) {
		applicationSnapshot, err := r.getApplicationSnapshotFromTestPipelineRun(ctx, pipelineRun)
		if err != nil {
			log.Error(err, " Failed to get ApplicationSnapshot for ",
				"PipelineRun.Name ", pipelineRun.Name, "PipelineRun.Namespace ", pipelineRun.Namespace)
			return ctrl.Result{}, nil
		}
		log.Info("The Test PipelineRun ", pipelineRun.Name, " for the ApplicationSnapshot ", applicationSnapshot.Name, " Succeeded! ")
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

// getIntegrationScenario get an IntegrationScenario for a given ApplicationSnapshot.
func (r *IntegrationReconciler) getIntegrationScenario(ctx context.Context, application *hasv1alpha1.Application) (*v1alpha1.IntegrationScenario, error) {
	integrationScenario := &v1alpha1.IntegrationScenario{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: application.Namespace,
		Name:      application.Name,
	}, integrationScenario)

	if err != nil {
		return nil, err
	}

	return integrationScenario, nil
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

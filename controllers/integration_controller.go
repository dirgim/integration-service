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

		application, err := r.getApplicationForComponent(ctx, component)
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
			application, err := r.getApplicationForIntegrationPipelineRun(ctx, pipelineRun)
			if err != nil {
				log.Error(err, " Failed to get Application for ",
					"PipelineRun.Name ", pipelineRun.Name, "PipelineRun.Namespace ", pipelineRun.Namespace)
				return ctrl.Result{}, nil
			}
			releaseLinks, err := r.getApplicationReleaseLinks(ctx, application)
			if err != nil {
				log.Error(err, " Failed to get ReleaseLinks for ",
					"Application.Name ", application.Name, "Application.Namespace ", application.Namespace)
				return ctrl.Result{}, nil
			}

			for _, releaseLink := range *releaseLinks {
				release, err := CreateRelease(applicationSnapshot, &releaseLink)
				err = r.Client.Create(ctx, release)
				if err != nil {
					log.Error(err, " Failed to create Release for ",
						"ApplicationSnapshot.Name ", applicationSnapshot.Name, " ApplicationSnapshot.Namespace ",
						applicationSnapshot.Namespace)
					return ctrl.Result{}, nil
				}
				log.Info("Created Release ", release.Name, " for ApplicationSnapshot ", applicationSnapshot.Name)
			}
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

// getApplicationForComponent loads from the cluster the Application referenced in the given Component. If the Component doesn't
// specify an Application or this is not found in the cluster, an error will be returned.
func (r *IntegrationReconciler) getApplicationForComponent(ctx context.Context, component *hasv1alpha1.Component) (*hasv1alpha1.Application, error) {
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

// getApplicationForIntegrationPipelineRun loads from the cluster the Application referenced in the given Component. If the Component doesn't
// specify an Application or this is not found in the cluster, an error will be returned.
func (r *IntegrationReconciler) getApplicationForIntegrationPipelineRun(ctx context.Context, pipelineRun *tektonv1beta1.PipelineRun) (*hasv1alpha1.Application, error) {
	application := &hasv1alpha1.Application{}
	applicationName := pipelineRun.Labels["test.appstudio.openshift.io/application"]
	err := r.Get(ctx, types.NamespacedName{
		Namespace: pipelineRun.Namespace,
		Name:      applicationName,
	}, application)

	if err != nil {
		return nil, err
	}

	return application, nil
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

// getApplicationComponents loads from the cluster the Components associated with the given Application. If the Application
// doesn't have any Components or this is not found in the cluster, an error will be returned.
func (r *IntegrationReconciler) getApplicationComponents(ctx context.Context, application *hasv1alpha1.Application) (*[]hasv1alpha1.Component, error) {
	applicationComponents := &hasv1alpha1.ComponentList{}
	opts := []client.ListOption{
		client.InNamespace(application.Namespace),
		client.MatchingFields{"spec.application": application.Name},
	}

	err := r.List(ctx, applicationComponents, opts...)
	if err != nil {
		return nil, err
	}

	return &applicationComponents.Items, nil
}

// getApplicationReleaseLinks returns the ReleaseLinks used by the application being processed. If matching
// ReleaseLinks are not found, an error will be returned.
func (r *IntegrationReconciler) getApplicationReleaseLinks(ctx context.Context, application *hasv1alpha1.Application) (*[]releasev1alpha1.ReleaseLink, error) {
	releaseLinks := &releasev1alpha1.ReleaseLinkList{}
	opts := []client.ListOption{
		client.InNamespace(application.Namespace),
		client.MatchingFields{"spec.application": application.Name},
	}

	err := r.List(ctx, releaseLinks, opts...)
	if err != nil {
		return nil, err
	}

	return &releaseLinks.Items, nil
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

// CreateApplicationSnapshot creates an ApplicationSnapshot for the given application components and the build pipelineRun.
func CreateApplicationSnapshot(component *hasv1alpha1.Component, applicationComponents *[]hasv1alpha1.Component, pipelineRun *tektonv1beta1.PipelineRun) (*releasev1alpha1.ApplicationSnapshot, error) {
	var images []releasev1alpha1.Image

	for _, applicationComponent := range *applicationComponents {
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

// CreateRelease creates a Release for the given ApplicationSnapshot and the ReleaseLink.
func CreateRelease(applicationSnapshot *releasev1alpha1.ApplicationSnapshot, releaseLink *releasev1alpha1.ReleaseLink) (*releasev1alpha1.Release, error) {
	return &releasev1alpha1.Release{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: applicationSnapshot.Name + "-",
			Namespace:    applicationSnapshot.Namespace,
		},
		Spec: releasev1alpha1.ReleaseSpec{
			ApplicationSnapshot: applicationSnapshot.Name,
			ReleaseLink:         releaseLink.Name,
		},
	}, nil
}

// setupReleaseLinkCache adds a new index field to be able to search ReleaseLinks by application.
func setupReleaseLinkCache(mgr ctrl.Manager) error {
	releaseLinkTargetIndexFunc := func(obj client.Object) []string {
		return []string{obj.(*releasev1alpha1.ReleaseLink).Spec.Application}
	}

	return mgr.GetCache().IndexField(context.Background(), &releasev1alpha1.ReleaseLink{},
		"spec.application", releaseLinkTargetIndexFunc)
}

// setupApplicationComponentCache adds a new index field to be able to search Components by application.
func setupApplicationComponentCache(mgr ctrl.Manager) error {
	releaseLinkTargetIndexFunc := func(obj client.Object) []string {
		return []string{obj.(*hasv1alpha1.Component).Spec.Application}
	}

	return mgr.GetCache().IndexField(context.Background(), &hasv1alpha1.Component{},
		"spec.application", releaseLinkTargetIndexFunc)
}

// SetupWithManager sets up the controller with the Manager.
func (r *IntegrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	err := setupReleaseLinkCache(mgr)
	if err != nil {
		return err
	}
	err = setupApplicationComponentCache(mgr)
	if err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&tektonv1beta1.PipelineRun{}).
		WithEventFilter(tekton.BuildOrPrelimPipelineRunSucceededPredicate()).
		Complete(r)
}

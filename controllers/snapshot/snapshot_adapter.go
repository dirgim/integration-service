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

package snapshot

import (
	"context"
	"fmt"
	appstudioshared "github.com/dirgim/managed-gitops/appstudio-shared/apis/appstudio.redhat.com/v1alpha1"
	"github.com/go-logr/logr"
	hasv1alpha1 "github.com/redhat-appstudio/application-service/api/v1alpha1"
	"github.com/redhat-appstudio/integration-service/controllers/results"
	"github.com/redhat-appstudio/integration-service/tekton"
	releasev1alpha1 "github.com/redhat-appstudio/release-service/api/v1alpha1"
	tektonv1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
)

// Adapter holds the objects needed to reconcile a Release.
type Adapter struct {
	snapshot    *appstudioshared.ApplicationSnapshot
	application *hasv1alpha1.Application
	logger      logr.Logger
	client      client.Client
	context     context.Context
}

// NewAdapter creates and returns an Adapter instance.
func NewAdapter(snapshot *appstudioshared.ApplicationSnapshot, application *hasv1alpha1.Application, logger logr.Logger, client client.Client,
	context context.Context) *Adapter {
	return &Adapter{
		snapshot:    snapshot,
		application: application,
		logger:      logger,
		client:      client,
		context:     context,
	}
}

// EnsureAllReleasesExist is an operation that will ensure that all pipeline Releases associated
// to the ApplicationSnapshot and the Application's ReleaseLinks exist.
// Otherwise, it will create new Releases for each ReleaseLink.
func (a *Adapter) EnsureAllReleasesExist() (results.OperationResult, error) {
	if a.snapshot.HasSucceeded() {
		a.logger.Info("Found ApplicationSnapshot hasn't finished testing, holding off on releases",
			"Application.Name", a.application.Name,
			"ApplicationSnapshot.Name", a.snapshot.Name)
		return results.ContinueProcessing()
	}

	releaseLinks, err := a.getAutoReleaseLinksForApplication(a.application)
	if err != nil {
		a.logger.Error(err, "Failed to get all ReleaseLinks",
			"Application.Name", a.application.Name,
			"Application.Namespace", a.application.Namespace)
		return results.RequeueOnErrorOrStop(a.updateStatus())
	}

	err = a.createMissingReleasesForReleaseLinks(releaseLinks, a.snapshot)
	if err != nil {
		a.logger.Error(err, "Failed to create new Releases for",
			"ApplicationSnapshot.Name", a.snapshot.Name,
			"ApplicationSnapshot.Namespace", a.snapshot.Namespace)
		return results.RequeueOnErrorOrStop(a.updateStatus())
	}

	return results.ContinueProcessing()
}

// compareApplicationSnapshots compares two ApplicationSnapshots and returns boolean true if their images match exactly.
func (a *Adapter) compareApplicationSnapshots(expectedApplicationSnapshot *appstudioshared.ApplicationSnapshot, foundApplicationSnapshot *appstudioshared.ApplicationSnapshot) bool {
	snapshotsHaveSameNumberOfImages :=
		len(expectedApplicationSnapshot.Spec.Components) == len(foundApplicationSnapshot.Spec.Components)
	allImagesMatch := true
	for _, component1 := range expectedApplicationSnapshot.Spec.Components {
		foundImage := false
		for _, component2 := range foundApplicationSnapshot.Spec.Components {
			if component2 == component1 {
				foundImage = true
			}
		}
		if !foundImage {
			allImagesMatch = false
		}

	}
	return snapshotsHaveSameNumberOfImages && allImagesMatch
}

// getAllApplicationReleaseLinks returns the ReleaseLinks used by the application being processed. If matching
// ReleaseLinks are not found, an error will be returned. A ReleaseLink will only be returned if it has the
// release.appstudio.openshift.io/auto-release label set to true or if it is missing the label entirely.
func (a *Adapter) getAutoReleaseLinksForApplication(application *hasv1alpha1.Application) (*[]releasev1alpha1.ReleaseLink, error) {
	releaseLinks := &releasev1alpha1.ReleaseLinkList{}
	labelSelector := labels.NewSelector()
	labelRequirement, err := labels.NewRequirement("release.appstudio.openshift.io/auto-release", selection.NotIn, []string{"false"})
	if err != nil {
		return nil, err
	}
	labelSelector = labelSelector.Add(*labelRequirement)

	opts := &client.ListOptions{
		Namespace:     application.Namespace,
		FieldSelector: fields.OneTermEqualSelector("spec.application", application.Name),
		LabelSelector: labelSelector,
	}

	err = a.client.List(a.context, releaseLinks, opts)
	if err != nil {
		return nil, err
	}

	return &releaseLinks.Items, nil
}

// getApplicationSnapshot returns the all ApplicationSnapshots in the Application's namespace nil if it's not found.
// In the case the List operation fails, an error will be returned.
func (a *Adapter) getReleasesWithApplicationSnapshot(applicationSnapshot *appstudioshared.ApplicationSnapshot) (*[]releasev1alpha1.Release, error) {
	releases := &releasev1alpha1.ReleaseList{}
	opts := []client.ListOption{
		client.InNamespace(applicationSnapshot.Namespace),
		client.MatchingFields{"spec.applicationSnapshot": applicationSnapshot.Name},
	}

	err := a.client.List(a.context, releases, opts...)
	if err != nil {
		return nil, err
	}

	return &releases.Items, nil
}

// getImagePullSpecFromPipelineRun gets the full image pullspec from the given build PipelineRun,
// In case the Image pullspec can't be can't be composed, an error will be returned.
func (a *Adapter) getImagePullSpecFromPipelineRun(pipelineRun *tektonv1beta1.PipelineRun) (string, error) {
	var err error
	outputImage, err := tekton.GetOutputImage(pipelineRun)
	if err != nil {
		return "", err
	}
	imageDigest, err := tekton.GetOutputImageDigest(pipelineRun)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s@%s", strings.Split(outputImage, ":")[0], imageDigest), nil
}

// createReleaseForReleaseLink creates the Release for a given ReleaseLink.
// In case the Release can't be created, an error will be returned.
func (a *Adapter) createReleaseForReleaseLink(releaseLink *releasev1alpha1.ReleaseLink, applicationSnapshot *appstudioshared.ApplicationSnapshot) (*releasev1alpha1.Release, error) {
	newRelease := &releasev1alpha1.Release{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: applicationSnapshot.Name + "-",
			Namespace:    applicationSnapshot.Namespace,
		},
		Spec: releasev1alpha1.ReleaseSpec{
			ApplicationSnapshot: applicationSnapshot.Name,
			ReleaseLink:         releaseLink.Name,
		},
	}
	err := a.client.Create(a.context, newRelease)
	if err != nil {
		return nil, err
	}

	return newRelease, nil
}

// createReleaseForReleaseLink checks if there's existing Releases for a given list of ReleaseLinks and creates new ones
// if they are missing. In case the Releases can't be created, an error will be returned.
func (a *Adapter) createMissingReleasesForReleaseLinks(releaseLinks *[]releasev1alpha1.ReleaseLink, applicationSnapshot *appstudioshared.ApplicationSnapshot) error {
	releases, err := a.getReleasesWithApplicationSnapshot(applicationSnapshot)
	if err != nil {
		return err
	}

	for _, releaseLink := range *releaseLinks {
		var existingRelease *releasev1alpha1.Release = nil
		for _, snapshotRelease := range *releases {
			if snapshotRelease.Spec.ReleaseLink == releaseLink.Name {
				existingRelease = &snapshotRelease
			}
		}
		if existingRelease != nil {
			a.logger.Info("Found existing Release",
				"ApplicationSnapshot.Name", applicationSnapshot.Name,
				"ReleaseLink.Name", releaseLink.Name,
				"Release.Name", existingRelease.Name)
		} else {
			newRelease, err := a.createReleaseForReleaseLink(&releaseLink, applicationSnapshot)
			if err != nil {
				return err
			}
			a.logger.Info("Created new Release",
				"Application.Name", a.application.Name,
				"ReleaseLink.Name", releaseLink.Name,
				"Release.Name", newRelease.Name)
		}
	}
	return nil
}

// updateStatus updates the status of the PipelineRun being processed.
func (a *Adapter) updateStatus() error {
	return a.client.Status().Update(a.context, a.snapshot)
}

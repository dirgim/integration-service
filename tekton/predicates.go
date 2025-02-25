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

package tekton

import (
	"github.com/redhat-appstudio/integration-service/helpers"
	"github.com/redhat-appstudio/operator-toolkit/metadata"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// IntegrationPipelineRunPredicate returns a predicate which filters out all objects except
// integration PipelineRuns that have just started or finished.
func IntegrationPipelineRunPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(createEvent event.CreateEvent) bool {
			return false
		},
		DeleteFunc: func(deleteEvent event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(genericEvent event.GenericEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return IsIntegrationPipelineRun(e.ObjectNew) &&
				(hasPipelineRunStateChangedToStarted(e.ObjectOld, e.ObjectNew) || hasPipelineRunStateChangedToFinished(e.ObjectOld, e.ObjectNew))
		},
	}
}

// BuildPipelineRunSignedAndSucceededPredicate returns a predicate which filters out all objects except
// Build PipelineRuns which have finished, been signed and haven't had a Snapshot created for them.
func BuildPipelineRunSignedAndSucceededPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(createEvent event.CreateEvent) bool {
			return false
		},
		DeleteFunc: func(deleteEvent event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(genericEvent event.GenericEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return IsBuildPipelineRun(e.ObjectNew) && isPipelineRunSigned(e.ObjectNew) &&
				helpers.HasPipelineRunSucceeded(e.ObjectNew) &&
				!metadata.HasAnnotation(e.ObjectNew, SnapshotNameLabel)
		},
	}
}

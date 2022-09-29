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

package gitops

import (
	"context"
	appstudioshared "github.com/redhat-appstudio/managed-gitops/appstudio-shared/apis/appstudio.redhat.com/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// getAllEnvironments gets all environments in the namespace
func getAllEnvironments(adapterClient client.Client, ctx context.Context, namespace string) (*[]appstudioshared.Environment, error) {
	environmentList := &appstudioshared.EnvironmentList{}
	opts := []client.ListOption{
		client.InNamespace(namespace),
	}
	err := adapterClient.List(ctx, environmentList, opts...)
	return &environmentList.Items, err
}

// GetEnvironment gets all environments in the namespace
func GetEnvironment(adapterClient client.Client, ctx context.Context, environmentName string, namespace string) (*appstudioshared.Environment, error) {
	environment := &appstudioshared.Environment{}
	err := adapterClient.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      environmentName,
	}, environment)

	if err != nil {
		return nil, err
	}
	return environment, nil
}

// FindAvailableEnvironments gets all environments that don't have a ParentEnvironment and are not tagged as ephemeral.
func FindAvailableEnvironments(adapterClient client.Client, ctx context.Context, namespace string) (*[]appstudioshared.Environment, error) {
	allEnvironments, err := getAllEnvironments(adapterClient, ctx, namespace)
	if err != nil {
		return nil, err
	}
	availableEnvironments := []appstudioshared.Environment{}
	for _, environment := range *allEnvironments {
		if environment.Spec.ParentEnvironment == "" {
			isEphemeral := false
			for _, tag := range environment.Spec.Tags {
				if tag == "ephemeral" {
					isEphemeral = true
					break
				}
			}
			if !isEphemeral {
				availableEnvironments = append(availableEnvironments, environment)
			}
		}
	}
	return &availableEnvironments, nil
}

// CreateEphemeralEnvironmentCopy creates a new ephemeral Environment by copying the provided Environment
func CreateEphemeralEnvironmentCopy(environment *appstudioshared.Environment) *appstudioshared.Environment {
	ephemeralEnvironment := &appstudioshared.Environment{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "ephemeral-" + environment.Name + "-",
			Namespace:    environment.Namespace,
		},
		Spec: appstudioshared.EnvironmentSpec{
			Type:               environment.Spec.Type,
			DisplayName:        "ephemeral-" + environment.Spec.DisplayName,
			DeploymentStrategy: environment.Spec.DeploymentStrategy,
			Configuration:      environment.Spec.Configuration,
			Tags:               []string{"ephemeral"},
		},
	}

	return ephemeralEnvironment
}

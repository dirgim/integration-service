/*
Copyright 2026 Red Hat Inc.

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

package buildpipeline

import (
	"encoding/json"
	"fmt"

	applicationapiv1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	"github.com/konflux-ci/integration-service/api/konflux/v1beta2"
	h "github.com/konflux-ci/integration-service/helpers"
	"github.com/konflux-ci/integration-service/loader"
	"github.com/konflux-ci/integration-service/pkg/keys"
	tektonconsts "github.com/konflux-ci/integration-service/tekton/consts"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var priorityNamespaceSequence int

var _ = Describe("Build pipeline priority class", func() {
	var (
		reconciler *Reconciler
		namespace  string
	)

	BeforeEach(func() {
		priorityNamespaceSequence++
		namespace = fmt.Sprintf("prio-%d", priorityNamespaceSequence)
		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})).To(Succeed())
		reconciler = NewIntegrationReconciler(k8sClient, &logf.Log, k8sClient.Scheme())
	})

	AfterEach(func() {
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}))).To(Succeed())
	})

	It("raises a post-merge root parent and leaves a branch end at the floor", func() {
		createComponent(namespace, "root")
		createComponent(namespace, "leaf")
		createNudgeConfig(namespace, nudge("root", "leaf"))

		root := createBuildPipelineRun(namespace, "root-push", "root", map[string]string{
			tektonconsts.PipelineAsCodeEventTypeLabel: "push",
		}, map[string]string{
			// A submitted severity does not select the class.
			h.CVEMaxSeverityAnnotation: "critical",
		})

		Expect(ensureBuildPriority(root)).To(Succeed())
		expectPriority(namespace, root.Name, h.PriorityClassPostMergeHigh, "")

		leaf := createBuildPipelineRun(namespace, "leaf-push", "leaf", map[string]string{
			tektonconsts.PipelineAsCodeEventTypeLabel: "push",
		}, nil)
		Expect(ensureBuildPriority(leaf)).To(Succeed())
		expectPriority(namespace, leaf.Name, h.PriorityClassPostMergeBackground, "")
	})

	It("uses today's class when the component is in no nudge", func() {
		createComponent(namespace, "plain")
		pipelineRun := createBuildPipelineRun(namespace, "plain-push", "plain", map[string]string{
			tektonconsts.PipelineAsCodeEventTypeLabel: "Push",
		}, nil)
		Expect(ensureBuildPriority(pipelineRun)).To(Succeed())
		expectPriority(namespace, pipelineRun.Name, h.PriorityClassPostMerge, "")

		preMerge := createBuildPipelineRun(namespace, "plain-pr", "plain", map[string]string{
			tektonconsts.PipelineAsCodeEventTypeLabel:   "pull_request",
			tektonconsts.PipelineAsCodePullRequestLabel: "15",
		}, nil)
		Expect(ensureBuildPriority(preMerge)).To(Succeed())
		expectPriority(namespace, preMerge.Name, h.PriorityClassPreMerge, "")
	})

	It("writes konflux-default for a component build on neither row", func() {
		createComponent(namespace, "plain")
		pipelineRun := createBuildPipelineRun(namespace, "other-event", "plain", map[string]string{
			tektonconsts.PipelineAsCodeEventTypeLabel: "incoming",
		}, nil)
		Expect(ensureBuildPriority(pipelineRun)).To(Succeed())
		expectPriority(namespace, pipelineRun.Name, h.PriorityClassDefault, "")
	})

	It("uses build-event when Pipelines as Code did not set an event type", func() {
		createComponent(namespace, "root")
		createNudgeConfig(namespace, nudge("root", "leaf"))
		pipelineRun := createBuildPipelineRun(namespace, "manual-pr", "root", nil, map[string]string{
			h.BuildEventAnnotation: "pull-request",
		})
		Expect(ensureBuildPriority(pipelineRun)).To(Succeed())
		expectPriority(namespace, pipelineRun.Name, h.PriorityClassPreMergeHigh, "")
	})

	It("leaves a MintMaker branch and a nudge PipelineRun on the class they arrived with", func() {
		createComponent(namespace, "root")
		createNudgeConfig(namespace, nudge("root", "leaf"))

		mintmaker := createBuildPipelineRun(namespace, "mintmaker", "root", map[string]string{
			tektonconsts.PipelineAsCodeEventTypeLabel: "push",
			h.KueuePriorityClassLabel:                 "konflux-mintmaker-triggered",
		}, map[string]string{
			tektonconsts.PipelineAsCodeSourceBranchAnnotation: "konflux/mintmaker/deps",
			tektonconsts.PipelineAsCodeSHATitleAnnotation:     "fix(security-critical): bump",
		})
		mintmaker.Labels[tektonconsts.PipelineAsCodePullRequestLabel] = "4"
		Expect(k8sClient.Update(ctx, mintmaker)).To(Succeed())
		Expect(ensureBuildPriority(mintmaker)).To(Succeed())
		expectPriority(namespace, mintmaker.Name, "konflux-mintmaker-triggered", "")
		expectNoCVERecord(namespace, "root")

		nudgeRun := createBuildPipelineRun(namespace, "nudge-run", "root", map[string]string{
			tektonconsts.NudgeTypeLabel:               tektonconsts.NudgePipelineRunTypeValue,
			h.KueuePriorityClassLabel:                 "konflux-nudge",
			tektonconsts.PipelineAsCodeEventTypeLabel: "push",
		}, nil)
		Expect(ensureBuildPriority(nudgeRun)).To(Succeed())
		expectPriority(namespace, nudgeRun.Name, "konflux-nudge", "")
	})

	It("records a pre-merge security subject and spends it on the post-merge build", func() {
		createComponent(namespace, "root")
		createNudgeConfig(namespace, nudge("root", "leaf"))

		preMerge := createBuildPipelineRun(namespace, "cve-pr", "root", map[string]string{
			tektonconsts.PipelineAsCodeEventTypeLabel:   "pull_request",
			tektonconsts.PipelineAsCodePullRequestLabel: "42",
		}, map[string]string{
			tektonconsts.PipelineAsCodeSHATitleAnnotation: "fix(security-critical): patch openssl",
			"keep-me": "yes",
		})
		Expect(ensureBuildPriority(preMerge)).To(Succeed())
		expectPriority(namespace, preMerge.Name, h.PriorityClassPreMergeCVECriticalHigh, string(h.CVESeverityCritical))
		expectCVERecord(namespace, "root", "42", h.CVESeverityCritical)

		retest := createBuildPipelineRun(namespace, "cve-retest", "root", map[string]string{
			tektonconsts.PipelineAsCodeEventTypeLabel:   "retest-all-comment",
			tektonconsts.PipelineAsCodePullRequestLabel: "42",
		}, map[string]string{
			tektonconsts.PipelineAsCodeSHATitleAnnotation: "chore: retrigger",
		})
		Expect(ensureBuildPriority(retest)).To(Succeed())
		expectPriority(namespace, retest.Name, h.PriorityClassPreMergeCVECriticalHigh, string(h.CVESeverityCritical))
		expectCVERecord(namespace, "root", "42", h.CVESeverityCritical)

		postMerge := createBuildPipelineRun(namespace, "cve-push", "root", map[string]string{
			tektonconsts.PipelineAsCodeEventTypeLabel:   "push",
			tektonconsts.PipelineAsCodePullRequestLabel: "42",
		}, map[string]string{
			tektonconsts.PipelineAsCodeSHATitleAnnotation: "fix(security): follow-up",
		})
		Expect(ensureBuildPriority(postMerge)).To(Succeed())
		expectPriority(namespace, postMerge.Name, h.PriorityClassCVECriticalHigh, string(h.CVESeverityCritical))
		expectNoCVERecord(namespace, "root")

		component := &applicationapiv1alpha1.Component{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "root", Namespace: namespace}, component)).To(Succeed())
		Expect(component.Annotations).To(HaveKeyWithValue("keep-me", "yes"))
	})

	It("does not record a pull request from a post-merge subject", func() {
		createComponent(namespace, "plain")
		push := createBuildPipelineRun(namespace, "push-subject", "plain", map[string]string{
			tektonconsts.PipelineAsCodeEventTypeLabel:   "push",
			tektonconsts.PipelineAsCodePullRequestLabel: "8",
		}, map[string]string{
			tektonconsts.PipelineAsCodeSHATitleAnnotation: "fix(security-critical): too late",
		})
		Expect(ensureBuildPriority(push)).To(Succeed())
		expectPriority(namespace, push.Name, h.PriorityClassPostMerge, "")
		expectNoCVERecord(namespace, "plain")
	})

	It("spends a listed pull request named by rebuild-pull-request", func() {
		createComponent(namespace, "leaf")
		createNudgeConfig(namespace, nudge("root", "leaf"))
		seedCVERecord(namespace, "leaf", h.CVERebuildPullRequests{Important: []string{"9"}})

		pipelineRun := createBuildPipelineRun(namespace, "rebuild", "leaf", map[string]string{
			tektonconsts.PipelineAsCodeEventTypeLabel: "push",
		}, map[string]string{
			h.RebuildPullRequestAnnotation: "9",
		})
		Expect(ensureBuildPriority(pipelineRun)).To(Succeed())
		expectPriority(namespace, pipelineRun.Name, h.PriorityClassCVE, string(h.CVESeverityImportant))
		expectNoCVERecord(namespace, "leaf")
	})

	It("does not set a priority class on a new-model build", func() {
		createComponent(namespace, "plain")
		pipelineRun := &tektonv1.PipelineRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "new-model-push",
				Namespace: namespace,
				Labels: map[string]string{
					keys.PipelineType().New:                   "build",
					keys.BuildComponent().New:                 "plain",
					tektonconsts.PipelineAsCodeEventTypeLabel: "push",
				},
			},
			Spec: minimalPipelineRunSpec(),
		}
		Expect(k8sClient.Create(ctx, pipelineRun)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pipelineRun.Name, Namespace: namespace}, pipelineRun)).To(Succeed())
		}).Should(Succeed())

		result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: pipelineRun.Name, Namespace: namespace}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))

		stored := &tektonv1.PipelineRun{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pipelineRun.Name, Namespace: namespace}, stored)).To(Succeed())
		Expect(stored.Labels[h.KueuePriorityClassLabel]).To(BeEmpty())
		Expect(stored.Annotations).NotTo(HaveKey(h.CreateSnapshotAnnotationName))
	})
})

func ensureBuildPriority(pipelineRun *tektonv1.PipelineRun) error {
	componentName := h.ComponentNameFromPipelineRun(pipelineRun)
	component := &applicationapiv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{Name: componentName, Namespace: pipelineRun.Namespace},
	}
	_ = k8sClient.Get(ctx, types.NamespacedName{Name: componentName, Namespace: pipelineRun.Namespace}, component)
	adapter := NewAdapterWithApplication(
		ctx,
		pipelineRun,
		component,
		&applicationapiv1alpha1.Application{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: pipelineRun.Namespace}},
		h.IntegrationLogger{Logger: logf.Log},
		loader.NewLoader(),
		k8sClient,
	)
	_, err := adapter.EnsureBuildPriorityClass()
	return err
}

func nudge(from, to string) v1beta2.NudgeRelationship {
	return v1beta2.NudgeRelationship{
		From: v1beta2.ComponentVersion{Component: from},
		To:   v1beta2.ComponentVersion{Component: to},
	}
}

func createComponent(namespace, name string) {
	component := &applicationapiv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				"keep-me": "yes",
			},
		},
		Spec: applicationapiv1alpha1.ComponentSpec{
			ComponentName: name,
			Application:   "app",
			Source: applicationapiv1alpha1.ComponentSource{
				ComponentSourceUnion: applicationapiv1alpha1.ComponentSourceUnion{
					GitSource: &applicationapiv1alpha1.GitSource{URL: "https://github.com/example/repo"},
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, component)).To(Succeed())
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &applicationapiv1alpha1.Component{})).To(Succeed())
	}).Should(Succeed())
}

func createNudgeConfig(namespace string, nudges ...v1beta2.NudgeRelationship) {
	config := &v1beta2.NudgeConfig{
		ObjectMeta: metav1.ObjectMeta{Name: v1beta2.NudgeConfigSingletonName, Namespace: namespace},
		Spec:       v1beta2.NudgeConfigSpec{Nudges: nudges},
	}
	Expect(k8sClient.Create(ctx, config)).To(Succeed())
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: config.Name, Namespace: namespace}, &v1beta2.NudgeConfig{})).To(Succeed())
	}).Should(Succeed())
}

func createBuildPipelineRun(namespace, name, componentName string, labels, annotations map[string]string) *tektonv1.PipelineRun {
	if labels == nil {
		labels = map[string]string{}
	}
	labels["pipelines.appstudio.openshift.io/type"] = "build"
	labels["appstudio.openshift.io/component"] = componentName
	pipelineRun := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: minimalPipelineRunSpec(),
	}
	Expect(k8sClient.Create(ctx, pipelineRun)).To(Succeed())
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, pipelineRun)).To(Succeed())
	}).Should(Succeed())
	return pipelineRun
}

func minimalPipelineRunSpec() tektonv1.PipelineRunSpec {
	return tektonv1.PipelineRunSpec{
		PipelineRef: &tektonv1.PipelineRef{
			Name: "build-pipeline-pass",
			ResolverRef: tektonv1.ResolverRef{
				Resolver: "bundle",
				Params: tektonv1.Params{
					{Name: "bundle", Value: tektonv1.ParamValue{Type: "string", StringVal: "quay.io/example/bundle:test"}},
					{Name: "name", Value: tektonv1.ParamValue{Type: "string", StringVal: "build"}},
				},
			},
		},
	}
}

func seedCVERecord(namespace, componentName string, lists h.CVERebuildPullRequests) {
	component := &applicationapiv1alpha1.Component{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: componentName, Namespace: namespace}, component)).To(Succeed())
	raw, err := json.Marshal(lists.Normalized())
	Expect(err).NotTo(HaveOccurred())
	patch := client.MergeFrom(component.DeepCopy())
	if component.Annotations == nil {
		component.Annotations = map[string]string{}
	}
	component.Annotations[h.CVERebuildPullRequestsAnnotation] = string(raw)
	Expect(k8sClient.Patch(ctx, component, patch)).To(Succeed())
}

func expectPriority(namespace, name, class, severity string) {
	Eventually(func(g Gomega) {
		pipelineRun := &tektonv1.PipelineRun{}
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, pipelineRun)).To(Succeed())
		g.Expect(pipelineRun.Labels).To(HaveKeyWithValue(h.KueuePriorityClassLabel, class))
		g.Expect(pipelineRun.Annotations[h.CVEMaxSeverityAnnotation]).To(Equal(severity))
	}).Should(Succeed())
}

func expectCVERecord(namespace, componentName, number string, severity h.CVESeverity) {
	Eventually(func(g Gomega) {
		lists := readComponentCVELists(g, namespace, componentName)
		got, found := lists.Lookup(number)
		g.Expect(found).To(BeTrue())
		g.Expect(got).To(Equal(severity))
	}).Should(Succeed())
}

func expectNoCVERecord(namespace, componentName string) {
	Eventually(func(g Gomega) {
		g.Expect(readComponentCVELists(g, namespace, componentName).Empty()).To(BeTrue())
	}).Should(Succeed())
}

func readComponentCVELists(g Gomega, namespace, componentName string) h.CVERebuildPullRequests {
	component := &unstructured.Unstructured{}
	component.SetGroupVersionKind(applicationapiv1alpha1.GroupVersion.WithKind("Component"))
	g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: componentName, Namespace: namespace}, component)).To(Succeed())
	if raw := component.GetAnnotations()[h.CVERebuildPullRequestsAnnotation]; raw != "" {
		var lists h.CVERebuildPullRequests
		g.Expect(json.Unmarshal([]byte(raw), &lists)).To(Succeed())
		return lists
	}
	status, found, err := unstructured.NestedFieldNoCopy(component.Object, "status", h.CVERebuildPullRequestsStatusField)
	g.Expect(err).NotTo(HaveOccurred())
	if !found || status == nil {
		return h.CVERebuildPullRequests{}
	}
	encoded, err := json.Marshal(status)
	g.Expect(err).NotTo(HaveOccurred())
	var lists h.CVERebuildPullRequests
	g.Expect(json.Unmarshal(encoded, &lists)).To(Succeed())
	return lists
}

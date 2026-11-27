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

package helpers_test

import (
	"testing"

	v1beta2 "github.com/konflux-ci/integration-service/api/konflux/v1beta2"
	"github.com/konflux-ci/integration-service/helpers"
	. "github.com/onsi/gomega"
)

func TestBuildPriorityRowForEvent(t *testing.T) {
	g := NewWithT(t)
	cases := []struct {
		eventType  string
		buildEvent string
		want       helpers.BuildPriorityRow
	}{
		{eventType: "push", want: helpers.BuildPriorityRowPostMerge},
		{eventType: "Push", want: helpers.BuildPriorityRowPostMerge},
		{eventType: "pull_request", want: helpers.BuildPriorityRowPreMerge},
		{eventType: "Merge Request", want: helpers.BuildPriorityRowPreMerge},
		{eventType: "Merge_Request", want: helpers.BuildPriorityRowPreMerge},
		{eventType: "retest-all-comment", want: helpers.BuildPriorityRowPreMerge},
		{eventType: "ok-to-test-comment", want: helpers.BuildPriorityRowPreMerge},
		{eventType: "incoming", want: helpers.BuildPriorityRowDefault},
		{buildEvent: "push", want: helpers.BuildPriorityRowPostMerge},
		{buildEvent: "pull-request", want: helpers.BuildPriorityRowPreMerge},
		{buildEvent: "Push", want: helpers.BuildPriorityRowDefault},
		{eventType: "push", buildEvent: "pull-request", want: helpers.BuildPriorityRowPostMerge},
		{want: helpers.BuildPriorityRowDefault},
	}
	for _, tc := range cases {
		g.Expect(helpers.BuildPriorityRowForEvent(tc.eventType, tc.buildEvent)).To(Equal(tc.want))
	}
}

func TestBuildStandingForComponent(t *testing.T) {
	g := NewWithT(t)
	nudges := []v1beta2.NudgeRelationship{
		{From: v1beta2.ComponentVersion{Component: "root"}, To: v1beta2.ComponentVersion{Component: "middle"}},
		{From: v1beta2.ComponentVersion{Component: "middle"}, To: v1beta2.ComponentVersion{Component: "leaf"}},
	}
	g.Expect(helpers.BuildStandingForComponent("root", nudges)).To(Equal(helpers.BuildStandingHigh))
	g.Expect(helpers.BuildStandingForComponent("middle", nudges)).To(Equal(helpers.BuildStandingNormal))
	g.Expect(helpers.BuildStandingForComponent("leaf", nudges)).To(Equal(helpers.BuildStandingBackground))
	g.Expect(helpers.BuildStandingForComponent("other", nudges)).To(Equal(helpers.BuildStandingNormal))
	g.Expect(helpers.BuildStandingForComponent("root", nil)).To(Equal(helpers.BuildStandingNormal))
}

func TestStandingAndCVEPriorityClasses(t *testing.T) {
	g := NewWithT(t)
	g.Expect(helpers.StandingPriorityClass(helpers.BuildPriorityRowPostMerge, helpers.BuildStandingHigh)).To(Equal(helpers.PriorityClassPostMergeHigh))
	g.Expect(helpers.StandingPriorityClass(helpers.BuildPriorityRowPostMerge, helpers.BuildStandingNormal)).To(Equal(helpers.PriorityClassPostMerge))
	g.Expect(helpers.StandingPriorityClass(helpers.BuildPriorityRowPostMerge, helpers.BuildStandingBackground)).To(Equal(helpers.PriorityClassPostMergeBackground))
	g.Expect(helpers.StandingPriorityClass(helpers.BuildPriorityRowPreMerge, helpers.BuildStandingHigh)).To(Equal(helpers.PriorityClassPreMergeHigh))
	g.Expect(helpers.StandingPriorityClass(helpers.BuildPriorityRowPreMerge, helpers.BuildStandingNormal)).To(Equal(helpers.PriorityClassPreMerge))
	g.Expect(helpers.StandingPriorityClass(helpers.BuildPriorityRowPreMerge, helpers.BuildStandingBackground)).To(Equal(helpers.PriorityClassDefault))

	g.Expect(helpers.CVEPriorityClass(helpers.BuildPriorityRowPostMerge, helpers.BuildStandingHigh, helpers.CVESeverityCritical)).To(Equal(helpers.PriorityClassCVECriticalHigh))
	g.Expect(helpers.CVEPriorityClass(helpers.BuildPriorityRowPostMerge, helpers.BuildStandingBackground, helpers.CVESeverityCritical)).To(Equal(helpers.PriorityClassCVECritical))
	g.Expect(helpers.CVEPriorityClass(helpers.BuildPriorityRowPostMerge, helpers.BuildStandingHigh, helpers.CVESeverityImportant)).To(Equal(helpers.PriorityClassCVEHigh))
	g.Expect(helpers.CVEPriorityClass(helpers.BuildPriorityRowPostMerge, helpers.BuildStandingNormal, helpers.CVESeverityLow)).To(Equal(helpers.PriorityClassCVE))
	g.Expect(helpers.CVEPriorityClass(helpers.BuildPriorityRowPreMerge, helpers.BuildStandingHigh, helpers.CVESeverityCritical)).To(Equal(helpers.PriorityClassPreMergeCVECriticalHigh))
	g.Expect(helpers.CVEPriorityClass(helpers.BuildPriorityRowPreMerge, helpers.BuildStandingBackground, helpers.CVESeverityModerate)).To(Equal(helpers.PriorityClassPreMergeCVE))
}

func TestSeverityFromCommitSubject(t *testing.T) {
	g := NewWithT(t)
	cases := []struct {
		subject  string
		severity helpers.CVESeverity
		ok       bool
	}{
		{subject: "fix(security-critical): openssl", severity: helpers.CVESeverityCritical, ok: true},
		{subject: "Fix(Security-Critical!): openssl", severity: helpers.CVESeverityCritical, ok: true},
		{subject: "fix(security): openssl", severity: helpers.CVESeverityImportant, ok: true},
		{subject: "fix(security!): openssl", severity: helpers.CVESeverityImportant, ok: true},
		{subject: "fix(security-critical):", severity: helpers.CVESeverityCritical, ok: true},
		{subject: "fix(bug): openssl", ok: false},
		{subject: " fix(security): leading space", ok: false},
		{subject: "chore: fix(security): not a prefix", ok: false},
	}
	for _, tc := range cases {
		severity, ok := helpers.SeverityFromCommitSubject(tc.subject)
		g.Expect(ok).To(Equal(tc.ok), tc.subject)
		g.Expect(severity).To(Equal(tc.severity), tc.subject)
	}
}

func TestIsMintMakerSourceBranch(t *testing.T) {
	g := NewWithT(t)
	g.Expect(helpers.IsMintMakerSourceBranch("konflux/mintmaker/foo")).To(BeTrue())
	g.Expect(helpers.IsMintMakerSourceBranch("refs/heads/konflux/mintmaker/foo")).To(BeTrue())
	g.Expect(helpers.IsMintMakerSourceBranch("konflux/mintmaker")).To(BeFalse())
	g.Expect(helpers.IsMintMakerSourceBranch("main")).To(BeFalse())
}

func TestCVERebuildPullRequestLists(t *testing.T) {
	g := NewWithT(t)
	lists := helpers.CVERebuildPullRequests{}

	added, changed := lists.Add("7", helpers.CVESeverityImportant)
	g.Expect(changed).To(BeTrue())
	again, changed := added.Add("7", helpers.CVESeverityCritical)
	g.Expect(changed).To(BeFalse())
	g.Expect(again.Equal(added)).To(BeTrue())

	severity, found := added.Lookup("7")
	g.Expect(found).To(BeTrue())
	g.Expect(severity).To(Equal(helpers.CVESeverityImportant))

	both := helpers.CVERebuildPullRequests{Critical: []string{"7", "7"}, Important: []string{"7"}, Low: []string{"3"}}
	severity, found = both.Lookup("7")
	g.Expect(found).To(BeTrue())
	g.Expect(severity).To(Equal(helpers.CVESeverityCritical))
	removed, severity, changed := both.Remove("7")
	g.Expect(changed).To(BeTrue())
	g.Expect(severity).To(Equal(helpers.CVESeverityCritical))
	g.Expect(removed.Critical).To(BeNil())
	g.Expect(removed.Important).To(BeNil())
	g.Expect(removed.Low).To(Equal([]string{"3"}))
}

func TestDecideCVERebuildLists(t *testing.T) {
	g := NewWithT(t)

	recorded := helpers.DecideCVERebuildLists(
		helpers.CVERebuildPullRequests{},
		helpers.BuildPriorityRowPreMerge,
		helpers.CVESeverityCritical,
		true,
		"42",
		"42",
	)
	g.Expect(recorded.UseCVE).To(BeTrue())
	g.Expect(recorded.Severity).To(Equal(helpers.CVESeverityCritical))
	g.Expect(recorded.Lists.Critical).To(Equal([]string{"42"}))

	kept := helpers.DecideCVERebuildLists(
		helpers.CVERebuildPullRequests{Important: []string{"42"}},
		helpers.BuildPriorityRowPreMerge,
		helpers.CVESeverityCritical,
		true,
		"42",
		"42",
	)
	g.Expect(kept.UseCVE).To(BeTrue())
	g.Expect(kept.Severity).To(Equal(helpers.CVESeverityImportant))
	g.Expect(kept.Lists.Critical).To(BeNil())
	g.Expect(kept.Lists.Important).To(Equal([]string{"42"}))

	laterCommit := helpers.DecideCVERebuildLists(
		helpers.CVERebuildPullRequests{Critical: []string{"42"}},
		helpers.BuildPriorityRowPreMerge,
		"",
		false,
		"42",
		"42",
	)
	g.Expect(laterCommit.UseCVE).To(BeTrue())
	g.Expect(laterCommit.Lists.Equal(helpers.CVERebuildPullRequests{Critical: []string{"42"}})).To(BeTrue())

	spent := helpers.DecideCVERebuildLists(
		helpers.CVERebuildPullRequests{Critical: []string{"42"}, Low: []string{"42"}},
		helpers.BuildPriorityRowPostMerge,
		helpers.CVESeverityImportant,
		true,
		"42",
		"42",
	)
	g.Expect(spent.UseCVE).To(BeTrue())
	g.Expect(spent.Severity).To(Equal(helpers.CVESeverityCritical))
	g.Expect(spent.Lists.Empty()).To(BeTrue())

	unlistedPush := helpers.DecideCVERebuildLists(
		helpers.CVERebuildPullRequests{},
		helpers.BuildPriorityRowPostMerge,
		helpers.CVESeverityCritical,
		true,
		"42",
		"42",
	)
	g.Expect(unlistedPush.UseCVE).To(BeFalse())
	g.Expect(unlistedPush.Lists.Empty()).To(BeTrue())

	rebuild := helpers.DecideCVERebuildLists(
		helpers.CVERebuildPullRequests{Important: []string{"9"}},
		helpers.BuildPriorityRowPostMerge,
		"",
		false,
		"",
		"9",
	)
	g.Expect(rebuild.UseCVE).To(BeTrue())
	g.Expect(rebuild.Severity).To(Equal(helpers.CVESeverityImportant))
	g.Expect(rebuild.Lists.Empty()).To(BeTrue())
}

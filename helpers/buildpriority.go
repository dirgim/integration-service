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

package helpers

import (
	"regexp"
	"slices"
	"strings"

	v1beta2 "github.com/konflux-ci/integration-service/api/konflux/v1beta2"
	"github.com/konflux-ci/integration-service/pkg/keys"
	tektonconsts "github.com/konflux-ci/integration-service/tekton/consts"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

const (
	// KueuePriorityClassLabel is the label tekton-kueue copies onto the Workload.
	KueuePriorityClassLabel = "kueue.x-k8s.io/priority-class"
	// CVEMaxSeverityAnnotation is the severity key copied from Component status
	// onto the build PipelineRun. The priority expression does not read it.
	CVEMaxSeverityAnnotation = "build.konflux-ci.dev/cve-max-severity"
	// BuildEventAnnotation selects the push or pull-request row when Pipelines as Code
	// did not set an event type.
	BuildEventAnnotation = "build.konflux-ci.dev/build-event"
	// RebuildPullRequestAnnotation names the pull request of a rebuild that is not a git event.
	RebuildPullRequestAnnotation = "build.konflux-ci.dev/rebuild-pull-request"
	// CVERebuildPullRequestsAnnotation stores status.cveRebuildPullRequests when the
	// Component CRD prunes that field. Status is authoritative when the field is present.
	CVERebuildPullRequestsAnnotation = "konflux-ci.dev/cve-rebuild-pull-requests"
	// CVERebuildPullRequestsStatusField is the Component status field from ADR 0075.
	CVERebuildPullRequestsStatusField = "cveRebuildPullRequests"

	// MintMakerBranchPrefix marks an automated dependency bump. Those builds keep
	// the class the priority expression assigned.
	MintMakerBranchPrefix = "konflux/mintmaker/"

	// PriorityClassDefault is the catch-all class and the pre-merge background floor.
	PriorityClassDefault = "konflux-default"
	// PriorityClassPostMergeHigh is a post-merge build of a nudge-graph root parent.
	PriorityClassPostMergeHigh = "konflux-post-merge-build-high"
	// PriorityClassPostMerge is today's post-merge build class.
	PriorityClassPostMerge = "konflux-post-merge-build"
	// PriorityClassPostMergeBackground is the post-merge floor for a branch end.
	PriorityClassPostMergeBackground = "konflux-post-merge-build-background"
	// PriorityClassPreMergeHigh is a pre-merge build of a nudge-graph root parent.
	PriorityClassPreMergeHigh = "konflux-pre-merge-build-high"
	// PriorityClassPreMerge is today's pre-merge build class.
	PriorityClassPreMerge = "konflux-pre-merge-build"

	// PriorityClassCVECriticalHigh is a post-merge critical rebuild of a root parent.
	PriorityClassCVECriticalHigh = "konflux-cve-critical-high"
	// PriorityClassCVECritical is a post-merge critical rebuild of any other component.
	PriorityClassCVECritical = "konflux-cve-critical"
	// PriorityClassCVEHigh is a post-merge non-critical rebuild of a root parent.
	PriorityClassCVEHigh = "konflux-cve-high"
	// PriorityClassCVE is a post-merge non-critical rebuild of any other component.
	PriorityClassCVE = "konflux-cve"
	// PriorityClassPreMergeCVECriticalHigh is a pre-merge critical rebuild of a root parent.
	PriorityClassPreMergeCVECriticalHigh = "konflux-pre-merge-cve-critical-high"
	// PriorityClassPreMergeCVECritical is a pre-merge critical rebuild of any other component.
	PriorityClassPreMergeCVECritical = "konflux-pre-merge-cve-critical"
	// PriorityClassPreMergeCVEHigh is a pre-merge non-critical rebuild of a root parent.
	PriorityClassPreMergeCVEHigh = "konflux-pre-merge-cve-high"
	// PriorityClassPreMergeCVE is a pre-merge non-critical rebuild of any other component.
	PriorityClassPreMergeCVE = "konflux-pre-merge-cve"
)

// BuildPriorityRow is the push or pre-merge band for a component build.
type BuildPriorityRow int

const (
	// BuildPriorityRowDefault is a component build on neither the push nor the pre-merge row.
	BuildPriorityRowDefault BuildPriorityRow = iota
	// BuildPriorityRowPostMerge is a push component build.
	BuildPriorityRowPostMerge
	// BuildPriorityRowPreMerge is a pull-request, merge-request, /retest, or /ok-to-test build.
	BuildPriorityRowPreMerge
)

// BuildStanding is the component's place in the namespace NudgeConfig.
type BuildStanding string

const (
	// BuildStandingHigh is a root parent: named by from and not by to.
	BuildStandingHigh BuildStanding = "high"
	// BuildStandingNormal is a branch parent, a component in no nudge, or a missing NudgeConfig.
	BuildStandingNormal BuildStanding = "normal"
	// BuildStandingBackground is a branch end: named by to and not by from.
	BuildStandingBackground BuildStanding = "background"
)

// CVESeverity is a key of status.cveRebuildPullRequests.
type CVESeverity string

const (
	// CVESeverityCritical is recorded from a fix(security-critical) subject.
	CVESeverityCritical CVESeverity = "critical"
	// CVESeverityImportant is recorded from a fix(security) subject.
	CVESeverityImportant CVESeverity = "important"
	// CVESeverityModerate is a severity the UI may set. It shares the non-critical class.
	CVESeverityModerate CVESeverity = "moderate"
	// CVESeverityLow is a severity the UI may set. It shares the non-critical class.
	CVESeverityLow CVESeverity = "low"
)

// CVERebuildPullRequests is the Component status object that lists pull requests
// whose builds take a CVE priority class. An empty list is omitted.
type CVERebuildPullRequests struct {
	Critical  []string `json:"critical,omitempty"`
	Important []string `json:"important,omitempty"`
	Moderate  []string `json:"moderate,omitempty"`
	Low       []string `json:"low,omitempty"`
}

// CVEListDecision is the list update and class input for one component build.
type CVEListDecision struct {
	Lists    CVERebuildPullRequests
	UseCVE   bool
	Severity CVESeverity
}

var (
	securityCriticalSubject = regexp.MustCompile(`(?i)^fix\(security-critical!?\):`)
	securitySubject         = regexp.MustCompile(`(?i)^fix\(security!?\):`)
)

// IsMintMakerSourceBranch reports whether the Pipelines as Code source branch
// is an automated MintMaker bump. A refs/heads/ prefix is ignored.
func IsMintMakerSourceBranch(branch string) bool {
	branch = strings.TrimPrefix(branch, "refs/heads/")
	return strings.HasPrefix(branch, MintMakerBranchPrefix)
}

// BuildPriorityRowForEvent maps the Pipelines as Code event type, or
// build.konflux-ci.dev/build-event when that type is absent, onto a row.
func BuildPriorityRowForEvent(eventType, buildEvent string) BuildPriorityRow {
	if strings.TrimSpace(eventType) != "" {
		switch strings.ToLower(strings.TrimSpace(eventType)) {
		case "push":
			return BuildPriorityRowPostMerge
		case "pull_request", "merge request", "merge_request", "retest-all-comment", "ok-to-test-comment":
			return BuildPriorityRowPreMerge
		default:
			return BuildPriorityRowDefault
		}
	}
	switch buildEvent {
	case "push":
		return BuildPriorityRowPostMerge
	case "pull-request":
		return BuildPriorityRowPreMerge
	default:
		return BuildPriorityRowDefault
	}
}

// BuildStandingForComponent returns the standing class for component in nudges.
// Matching uses the component name and ignores version. A nil or empty graph is normal.
func BuildStandingForComponent(component string, nudges []v1beta2.NudgeRelationship) BuildStanding {
	if component == "" {
		return BuildStandingNormal
	}
	var from, to bool
	for _, nudge := range nudges {
		if nudge.From.Component == component {
			from = true
		}
		if nudge.To.Component == component {
			to = true
		}
	}
	switch {
	case from && !to:
		return BuildStandingHigh
	case !from && to:
		return BuildStandingBackground
	default:
		return BuildStandingNormal
	}
}

// StandingPriorityClass is the WorkloadPriorityClass for a row and standing class.
func StandingPriorityClass(row BuildPriorityRow, standing BuildStanding) string {
	switch row {
	case BuildPriorityRowPostMerge:
		switch standing {
		case BuildStandingHigh:
			return PriorityClassPostMergeHigh
		case BuildStandingBackground:
			return PriorityClassPostMergeBackground
		default:
			return PriorityClassPostMerge
		}
	case BuildPriorityRowPreMerge:
		switch standing {
		case BuildStandingHigh:
			return PriorityClassPreMergeHigh
		case BuildStandingBackground:
			return PriorityClassDefault
		default:
			return PriorityClassPreMerge
		}
	default:
		return PriorityClassDefault
	}
}

// CVEPriorityClass is the WorkloadPriorityClass for a CVE rebuild.
// important, moderate, and low share the non-critical pair. A root parent uses the high class of the pair.
func CVEPriorityClass(row BuildPriorityRow, standing BuildStanding, severity CVESeverity) string {
	high := standing == BuildStandingHigh
	critical := severity == CVESeverityCritical
	switch row {
	case BuildPriorityRowPostMerge:
		switch {
		case critical && high:
			return PriorityClassCVECriticalHigh
		case critical:
			return PriorityClassCVECritical
		case high:
			return PriorityClassCVEHigh
		default:
			return PriorityClassCVE
		}
	case BuildPriorityRowPreMerge:
		switch {
		case critical && high:
			return PriorityClassPreMergeCVECriticalHigh
		case critical:
			return PriorityClassPreMergeCVECritical
		case high:
			return PriorityClassPreMergeCVEHigh
		default:
			return PriorityClassPreMergeCVE
		}
	default:
		return PriorityClassDefault
	}
}

// SeverityFromCommitSubject matches the start of a Pipelines as Code sha-title.
// The match is case-insensitive. security-critical is tested before security.
// A ! immediately before the colon still matches.
func SeverityFromCommitSubject(subject string) (CVESeverity, bool) {
	switch {
	case securityCriticalSubject.MatchString(subject):
		return CVESeverityCritical, true
	case securitySubject.MatchString(subject):
		return CVESeverityImportant, true
	default:
		return "", false
	}
}

// Empty reports whether every severity list is empty.
func (l CVERebuildPullRequests) Empty() bool {
	return len(l.Critical) == 0 && len(l.Important) == 0 && len(l.Moderate) == 0 && len(l.Low) == 0
}

// Normalized returns a copy with duplicates removed and numbers sorted.
// Empty lists are nil so they can be omitted from JSON.
func (l CVERebuildPullRequests) Normalized() CVERebuildPullRequests {
	return CVERebuildPullRequests{
		Critical:  uniqueSorted(l.Critical),
		Important: uniqueSorted(l.Important),
		Moderate:  uniqueSorted(l.Moderate),
		Low:       uniqueSorted(l.Low),
	}
}

// Equal reports whether both objects contain the same numbers under the same keys.
func (l CVERebuildPullRequests) Equal(other CVERebuildPullRequests) bool {
	a := l.Normalized()
	b := other.Normalized()
	return slices.Equal(a.Critical, b.Critical) &&
		slices.Equal(a.Important, b.Important) &&
		slices.Equal(a.Moderate, b.Moderate) &&
		slices.Equal(a.Low, b.Low)
}

// Lookup returns the highest severity key that contains number.
func (l CVERebuildPullRequests) Lookup(number string) (CVESeverity, bool) {
	if number == "" {
		return "", false
	}
	switch {
	case slices.Contains(l.Critical, number):
		return CVESeverityCritical, true
	case slices.Contains(l.Important, number):
		return CVESeverityImportant, true
	case slices.Contains(l.Moderate, number):
		return CVESeverityModerate, true
	case slices.Contains(l.Low, number):
		return CVESeverityLow, true
	default:
		return "", false
	}
}

// Add appends number under severity when it is absent from every list.
// A number that is already recorded stays on its existing key.
func (l CVERebuildPullRequests) Add(number string, severity CVESeverity) (CVERebuildPullRequests, bool) {
	if number == "" {
		return l, false
	}
	if _, found := l.Lookup(number); found {
		return l, false
	}
	next := l
	switch severity {
	case CVESeverityCritical:
		next.Critical = append(slices.Clone(l.Critical), number)
	case CVESeverityImportant:
		next.Important = append(slices.Clone(l.Important), number)
	case CVESeverityModerate:
		next.Moderate = append(slices.Clone(l.Moderate), number)
	case CVESeverityLow:
		next.Low = append(slices.Clone(l.Low), number)
	default:
		return l, false
	}
	return next.Normalized(), true
}

// Remove deletes number from every list and returns the highest key that contained it.
func (l CVERebuildPullRequests) Remove(number string) (CVERebuildPullRequests, CVESeverity, bool) {
	severity, found := l.Lookup(number)
	if !found {
		return l, "", false
	}
	next := CVERebuildPullRequests{
		Critical:  without(l.Critical, number),
		Important: without(l.Important, number),
		Moderate:  without(l.Moderate, number),
		Low:       without(l.Low, number),
	}
	return next.Normalized(), severity, true
}

// DecideCVERebuildLists applies the ADR 0075 list rules for one component build.
// A pre-merge subject records pacPR when that number is absent. A post-merge build
// spends matchedPR. rebuild-pull-request is never added; pass it only as matchedPR.
func DecideCVERebuildLists(current CVERebuildPullRequests, row BuildPriorityRow, subject CVESeverity, subjectMatched bool, pacPR, matchedPR string) CVEListDecision {
	next := current
	if row == BuildPriorityRowPreMerge && subjectMatched && pacPR != "" {
		if updated, changed := next.Add(pacPR, subject); changed {
			next = updated
		}
	}
	decision := CVEListDecision{Lists: next}
	if matchedPR == "" {
		return decision
	}
	severity, found := next.Lookup(matchedPR)
	if !found {
		return decision
	}
	if row == BuildPriorityRowPostMerge {
		if updated, removedSeverity, changed := next.Remove(matchedPR); changed {
			next = updated
			severity = removedSeverity
		}
		decision.Lists = next
	}
	decision.UseCVE = true
	decision.Severity = severity
	return decision
}

func uniqueSorted(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := slices.Clone(values)
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func without(values []string, number string) []string {
	if len(values) == 0 {
		return nil
	}
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if value != number {
			filtered = append(filtered, value)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

// LeavesPriorityClass reports whether this PipelineRun keeps the class it already has.
// A nudge, a test, a release, and a MintMaker branch are left unchanged.
func LeavesPriorityClass(pipelineRun *tektonv1.PipelineRun) bool {
	pipelineType, found := keys.GetLabel(pipelineRun, keys.PipelineType())
	if !found || pipelineType != tektonconsts.PipelineRunBuildType {
		return true
	}
	if pipelineRunLabel(pipelineRun, tektonconsts.NudgeTypeLabel) == tektonconsts.NudgePipelineRunTypeValue {
		return true
	}
	return IsMintMakerSourceBranch(pipelineRunAnnotation(pipelineRun, tektonconsts.PipelineAsCodeSourceBranchAnnotation))
}

// ComponentNameFromPipelineRun returns the build component label, including the literal konflux-ci.dev key.
func ComponentNameFromPipelineRun(pipelineRun *tektonv1.PipelineRun) string {
	if name, found := keys.GetLabel(pipelineRun, keys.BuildComponent()); found && name != "" {
		return name
	}
	return pipelineRunLabel(pipelineRun, "konflux-ci.dev/component")
}

// PacPullRequest returns pipelinesascode.tekton.dev/pull-request.
func PacPullRequest(pipelineRun *tektonv1.PipelineRun) string {
	return pipelineRunLabel(pipelineRun, tektonconsts.PipelineAsCodePullRequestLabel)
}

// MatchedPullRequest returns the pull-request label, or rebuild-pull-request when that label is absent.
func MatchedPullRequest(pipelineRun *tektonv1.PipelineRun) string {
	if number := PacPullRequest(pipelineRun); number != "" {
		return number
	}
	return pipelineRunAnnotation(pipelineRun, RebuildPullRequestAnnotation)
}

// PipelineRunLabel returns a label value, or an empty string when the label is absent.
func PipelineRunLabel(pipelineRun *tektonv1.PipelineRun, key string) string {
	return pipelineRunLabel(pipelineRun, key)
}

// PipelineRunAnnotation returns an annotation value, or an empty string when the annotation is absent.
func PipelineRunAnnotation(pipelineRun *tektonv1.PipelineRun, key string) string {
	return pipelineRunAnnotation(pipelineRun, key)
}

func pipelineRunLabel(pipelineRun *tektonv1.PipelineRun, key string) string {
	if pipelineRun == nil || pipelineRun.Labels == nil {
		return ""
	}
	return pipelineRun.Labels[key]
}

func pipelineRunAnnotation(pipelineRun *tektonv1.PipelineRun, key string) string {
	if pipelineRun == nil || pipelineRun.Annotations == nil {
		return ""
	}
	return pipelineRun.Annotations[key]
}

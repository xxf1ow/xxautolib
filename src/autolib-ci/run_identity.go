package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

const ciGitHubRepository = "xxf1ow/xxautolib"

type ciRunIdentity struct {
	Repository  string `json:"repository"`
	Ref         string `json:"ref"`
	SHA         string `json:"sha"`
	CheckoutSHA string `json:"checkout_sha"`
	RunID       string `json:"run_id"`
	Attempt     string `json:"attempt"`
}

func validateCIRunIdentity(ctx context.Context, sourceRoot string, run ciRunIdentity) error {
	if run.Repository != ciGitHubRepository || !commitPattern.MatchString(run.SHA) || run.CheckoutSHA != run.SHA || !ciRunNumber(run.RunID) || !ciRunNumber(run.Attempt) {
		return errors.New("CI requires the source repository, matching event/checkout SHA and positive run ID/attempt")
	}
	if !strings.HasPrefix(run.Ref, "refs/heads/") || run.Ref == "refs/heads/" {
		return errors.New("CI requires a same-repository ordinary branch ref")
	}
	if err := exec.CommandContext(ctx, "git", "check-ref-format", run.Ref).Run(); err != nil {
		return errors.New("CI branch ref is invalid")
	}
	output, err := exec.CommandContext(ctx, "git", "-C", sourceRoot, "rev-parse", "HEAD").Output()
	if err != nil {
		return fmt.Errorf("read actual checkout HEAD: %w", err)
	}
	if strings.TrimSpace(string(output)) != run.SHA {
		return errors.New("actual checkout HEAD does not match run SHA")
	}
	return nil
}

func loadCIWork(ctx context.Context, o ciRunOptions) (ciPlan, ciRunIdentity, *ciPlatformPlan, string, string, error) {
	var plan ciPlan
	var run ciRunIdentity
	fail := func(err error) (ciPlan, ciRunIdentity, *ciPlatformPlan, string, string, error) {
		return plan, run, nil, "", "", err
	}
	for name, value := range map[string]string{"source-root": o.SourceRoot, "plan": o.PlanFile, "run-identity": o.RunIdentityFile, "platform": o.Platform, "work-root": o.WorkRoot} {
		if strings.TrimSpace(value) == "" {
			return fail(fmt.Errorf("--%s is required", name))
		}
	}
	if err := decodeStrictJSONFile(o.PlanFile, &plan); err != nil {
		return fail(err)
	}
	if err := decodeStrictJSONFile(o.RunIdentityFile, &run); err != nil {
		return fail(err)
	}
	if err := validateCIRunIdentity(ctx, o.SourceRoot, run); err != nil {
		return fail(err)
	}
	source, err := filepath.Abs(o.SourceRoot)
	if err != nil {
		return fail(err)
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return fail(err)
	}
	work, err := filepath.Abs(o.WorkRoot)
	if err != nil {
		return fail(err)
	}
	work, err = resolveExistingPrefix(work)
	if err != nil {
		return fail(err)
	}
	expected := filepath.Join(source, "build", "ci", run.RunID+"-"+run.Attempt, o.Platform)
	if work != expected {
		return fail(fmt.Errorf("CI work root must be %s", expected))
	}
	var selected *ciPlatformPlan
	for i := range plan.Platforms {
		if plan.Platforms[i].ID == o.Platform {
			if selected != nil {
				return fail(errors.New("duplicate selected platform"))
			}
			selected = &plan.Platforms[i]
		}
	}
	if selected == nil {
		return fail(fmt.Errorf("platform %s is not selected", o.Platform))
	}
	if _, _, err := ciTarget(o.Platform); err != nil {
		return fail(err)
	}
	if len(selected.Roots) == 0 || len(selected.Description.Modules) == 0 {
		return fail(errors.New("selected platform has no qualified roots"))
	}
	return plan, run, selected, source, work, nil
}

package events

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"github.com/runatlantis/atlantis/server/events/models"
	"github.com/runatlantis/atlantis/server/events/runtime"
	"github.com/runatlantis/atlantis/server/events/runtime/policy"
)

func NewPolicyCheckProjectCommandRunner(p *DefaultProjectCommandRunner) ProjectCommandRunner {
	return &PolicyCheckProjectCommandRunner{
		ProjectCommandRunner: p,
		PolicyCheckStepRunner: runtime.NewPolicyCheckStepRunner(
			policy.NewConfTestExecutorWorkflow(),
		),
	}
}

// PolicyCheckProjectCommandRunner implements ProjectCommandRunner.
type PolicyCheckProjectCommandRunner struct {
	ProjectCommandRunner  *DefaultCommandRunner
	PolicyCheckStepRunner PolicyCheckStepRunner
}

// PolicyCheckProjectCommandRunner adds PolicyCheck to the ProjectCommandRunner
// interface for a specific TF project.
type PolicyCheckerProjectCommandRunner interface {
	ProjectCommandRunner
	// PolicyCheck runs OPA defined policies for the project desribed by ctx.
	PolicyCheck(ctx models.ProjectCommandContext) models.ProjectResult
}

// PolicyCheck evaluates policies defined with Rego for the project described by ctx.
func (p *PolicyCheckProjectCommandRunner) PolicyCheck(ctx models.ProjectCommandContext) models.ProjectResult {
	policySuccess, failure, err := p.doPolicyCheck(ctx)
	return models.ProjectResult{
		Command:            models.PolicyCheckCommand,
		PolicyCheckSuccess: policySuccess,
		Error:              err,
		Failure:            failure,
		RepoRelDir:         ctx.RepoRelDir,
		Workspace:          ctx.Workspace,
		ProjectName:        ctx.ProjectName,
	}
}

func (p *PolicyCheckProjectCommandRunner) doPolicyCheck(ctx models.ProjectCommandContext) (*models.PolicyCheckSuccess, string, error) {
	// Acquire Atlantis lock for this repo/dir/workspace.
	lockAttempt, err := p.Locker.TryLock(ctx.Log, ctx.Pull, ctx.User, ctx.Workspace, models.NewProject(ctx.Pull.BaseRepo.FullName, ctx.RepoRelDir))
	if err != nil {
		return nil, "", errors.Wrap(err, "acquiring lock")
	}
	if !lockAttempt.LockAcquired {
		return nil, lockAttempt.LockFailureReason, nil
	}
	ctx.Log.Debug("acquired lock for project")

	// Acquire internal lock for the directory we're going to operate in.
	unlockFn, err := p.WorkingDirLocker.TryLock(ctx.Pull.BaseRepo.FullName, ctx.Pull.Num, ctx.Workspace)
	if err != nil {
		return nil, "", err
	}
	defer unlockFn()

	// Clone is idempotent so okay to run even if the repo was already cloned.
	repoDir, hasDiverged, cloneErr := p.WorkingDir.Clone(ctx.Log, ctx.HeadRepo, ctx.Pull, ctx.Workspace)
	if cloneErr != nil {
		if unlockErr := lockAttempt.UnlockFn(); unlockErr != nil {
			ctx.Log.Err("error unlocking state after policy_check error: %v", unlockErr)
		}
		return nil, "", cloneErr
	}
	projAbsPath := filepath.Join(repoDir, ctx.RepoRelDir)
	if _, err = os.Stat(projAbsPath); os.IsNotExist(err) {
		return nil, "", DirNotExistErr{RepoRelDir: ctx.RepoRelDir}
	}

	outputs, err := p.runSteps(ctx.Steps, ctx, projAbsPath)
	if err != nil {
		if unlockErr := lockAttempt.UnlockFn(); unlockErr != nil {
			ctx.Log.Err("error unlocking state after policy_check error: %v", unlockErr)
		}
		return nil, "", fmt.Errorf("%s\n%s", err, strings.Join(outputs, "\n"))
	}

	return &models.PolicyCheckSuccess{
		LockURL:           p.LockURLGenerator.GenerateLockURL(lockAttempt.LockKey),
		PolicyCheckOutput: strings.Join(outputs, "\n"),
		RePlanCmd:         ctx.RePlanCmd,
		ApplyCmd:          ctx.ApplyCmd,
		HasDiverged:       hasDiverged,
	}, "", nil
}

func (p *PolicyCheckProjectCommandRunner) runSteps(steps []valid.Steps, ctx *models.ProjectCommandContext) ([]string, error) {
	var outputs []string
	envs := make(map[string]string)
	for _, step := range steps {
		var out string
		var err error
		switch step.StepName {
		case "init":
			out, err = p.ProjectCommandRunner.InitStepRunner.Run(ctx, step.ExtraArgs, absPath, envs)
		case "plan":
			out, err = p.ProjectCommandRunner.PlanStepRunner.Run(ctx, step.ExtraArgs, absPath, envs)
		case "policy_check":
			out, err = p.PolicyCheckStepRunner.Run(ctx, ctx.PolicySets, step.ExtraArgs, absPath, envs)
		case "apply":
			out, err = p.ProjectCommandRunner.ApplyStepRunner.Run(ctx, step.ExtraArgs, absPath, envs)
		case "run":
			out, err = p.ProjectCommandRunner.RunStepRunner.Run(ctx, step.RunCommand, absPath, envs)
		case "env":
			out, err = p.ProjectCommandRunner.EnvStepRunner.Run(ctx, step.RunCommand, step.EnvVarValue, absPath, envs)
			envs[step.EnvVarName] = out
			// We reset out to the empty string because we don't want it to
			// be printed to the PR, it's solely to set the environment variable.
			out = ""
		}

		if out != "" {
			outputs = append(outputs, out)
		}
		if err != nil {
			return outputs, err
		}
	}
	return outputs, nil
}

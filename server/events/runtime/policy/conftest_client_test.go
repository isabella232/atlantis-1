package policy

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hashicorp/go-version"
	. "github.com/petergtz/pegomock"
	"github.com/runatlantis/atlantis/server/events/models"
	"github.com/runatlantis/atlantis/server/events/yaml/valid"
	"github.com/runatlantis/atlantis/server/events/runtime/cache/mocks"
	models_mocks "github.com/runatlantis/atlantis/server/events/runtime/models/mocks"
	conftest_mocks "github.com/runatlantis/atlantis/server/events/runtime/policy/mocks"
	terraform_mocks "github.com/runatlantis/atlantis/server/events/terraform/mocks"
	"github.com/runatlantis/atlantis/server/logging"
	. "github.com/runatlantis/atlantis/testing"
)

func TestConfTestVersionDownloader(t *testing.T) {

	version, _ := version.NewVersion("0.21.0")
	destPath := "some/path"

	fullURL := fmt.Sprintf("https://github.com/open-policy-agent/conftest/releases/download/v0.21.0/conftest_0.21.0_%s_x86_64.tar.gz?checksum=file:https://github.com/open-policy-agent/conftest/releases/download/v0.21.0/checksums.txt", strings.Title(runtime.GOOS))

	RegisterMockTestingT(t)

	mockDownloader := terraform_mocks.NewMockDownloader()

	subject := ConfTestVersionDownloader{downloader: mockDownloader}

	t.Run("success", func(t *testing.T) {

		When(mockDownloader.GetFile(EqString(destPath), EqString(fullURL))).ThenReturn(nil)
		binPath, err := subject.downloadConfTestVersion(version, destPath)

		mockDownloader.VerifyWasCalledOnce().GetAny(EqString(destPath), EqString(fullURL))

		Ok(t, err)

		Assert(t, binPath == filepath.Join(destPath, "conftest"), "expected binpath")
	})

	t.Run("error", func(t *testing.T) {

		When(mockDownloader.GetAny(EqString(destPath), EqString(fullURL))).ThenReturn(errors.New("err"))
		_, err := subject.downloadConfTestVersion(version, destPath)

		Assert(t, err != nil, "err is expected")
	})
}

func TestEnsureExecutorVersion(t *testing.T) {

	defaultVersion, _ := version.NewVersion("1.0")
	expectedPath := "some/path"

	RegisterMockTestingT(t)

	mockCache := mocks.NewMockExecutionVersionCache()
	log := logging.NewNoopLogger()

	t.Run("no specified version or default version", func(t *testing.T) {
		subject := &ConfTestExecutorWorkflow{
			VersionCache: mockCache,
		}

		_, err := subject.EnsureExecutorVersion(log, nil)

		Assert(t, err != nil, "expected error finding version")
	})

	t.Run("use default version", func(t *testing.T) {
		subject := &ConfTestExecutorWorkflow{
			VersionCache:           mockCache,
			DefaultConftestVersion: defaultVersion,
		}

		When(mockCache.Get(defaultVersion)).ThenReturn(expectedPath, nil)

		path, err := subject.EnsureExecutorVersion(log, nil)

		Ok(t, err)

		Assert(t, path == expectedPath, "path is expected")
	})

	t.Run("use specified version", func(t *testing.T) {
		subject := &ConfTestExecutorWorkflow{
			VersionCache:           mockCache,
			DefaultConftestVersion: defaultVersion,
		}

		versionInput, _ := version.NewVersion("2.0")

		When(mockCache.Get(versionInput)).ThenReturn(expectedPath, nil)

		path, err := subject.EnsureExecutorVersion(log, versionInput)

		Ok(t, err)

		Assert(t, path == expectedPath, "path is expected")
	})

	t.Run("cache error", func(t *testing.T) {
		subject := &ConfTestExecutorWorkflow{
			VersionCache:           mockCache,
			DefaultConftestVersion: defaultVersion,
		}

		versionInput, _ := version.NewVersion("2.0")

		When(mockCache.Get(versionInput)).ThenReturn(expectedPath, errors.New("some err"))

		_, err := subject.EnsureExecutorVersion(log, versionInput)

		Assert(t, err != nil, "path is expected")
	})
}

func TestRun(t *testing.T) {

	RegisterMockTestingT(t)
	mockResolver := conftest_mocks.NewMockSourceResolver()
	mockExec := models_mocks.NewMockExec()

	subject := &ConfTestExecutorWorkflow{
		SourceResolver: mockResolver,
		Exec:           mockExec,
	}

	policySetPath1 := "/some/path"
	localPolicySetPath1 := "/tmp/some/path"

	policySetPath2 := "/some/path2"
	localPolicySetPath2 := "/tmp/some/path2"
	executablePath := "/usr/bin/conftest"
	envs := map[string]string{
		"key": "val",
	}
	workdir := "/some_workdir"

	policySet1 := valid.PolicySet{
		Source: valid.LocalPolicySet,
		Path:   policySetPath1,
	}

	policySet2 := valid.PolicySet{
		Source: valid.LocalPolicySet,
		Path:   policySetPath2,
	}

	ctx := models.ProjectCommandContext{
		PolicySets: valid.PolicySets{
			PolicySets: []valid.PolicySet{
				policySet1,
				policySet2,
			},
		},
		ProjectName: "testproj",
		Workspace:   "default",
	}

	t.Run("success", func(t *testing.T) {

		expectedResult := "Success"
		expectedArgs := []string{executablePath, "test", "-p", localPolicySetPath1, "-p", localPolicySetPath2, "/some_workdir/testproj-default.json"}

		When(mockResolver.Resolve(policySet1)).ThenReturn(localPolicySetPath1, nil)
		When(mockResolver.Resolve(policySet2)).ThenReturn(localPolicySetPath2, nil)

		When(mockExec.CombinedOutput(expectedArgs, envs, workdir)).ThenReturn(expectedResult, nil)

		result, err := subject.Run(ctx, executablePath, envs, workdir)

		Ok(t, err)

		Assert(t, result == expectedResult, "result is expected")

	})

	t.Run("error resolving one policy source", func(t *testing.T) {

		expectedResult := "Success"
		expectedArgs := []string{executablePath, "test", "-p", localPolicySetPath1, "/some_workdir/testproj-default.json"}

		When(mockResolver.Resolve(policySet1)).ThenReturn(localPolicySetPath1, nil)
		When(mockResolver.Resolve(policySet2)).ThenReturn("", errors.New("err"))

		When(mockExec.CombinedOutput(expectedArgs, envs, workdir)).ThenReturn(expectedResult, nil)

		result, err := subject.Run(ctx, executablePath, envs, workdir)

		Ok(t, err)

		Assert(t, result == expectedResult, "result is expected")

	})

	t.Run("error resolving both policy sources", func(t *testing.T) {

		expectedResult := "Success"
		expectedArgs := []string{executablePath, "test", "-p", localPolicySetPath1, "/some_workdir/testproj-default.json"}

		When(mockResolver.Resolve(policySet1)).ThenReturn("", errors.New("err"))
		When(mockResolver.Resolve(policySet2)).ThenReturn("", errors.New("err"))

		When(mockExec.CombinedOutput(expectedArgs, envs, workdir)).ThenReturn(expectedResult, nil)

		result, err := subject.Run(ctx, executablePath, envs, workdir)

		Ok(t, err)

		Assert(t, result == "", "result is expected")

	})

	t.Run("error running cmd", func(t *testing.T) {
		expectedArgs := []string{executablePath, "test", "-p", localPolicySetPath1, "-p", localPolicySetPath2, "/some_workdir/testproj-default.json"}

		When(mockResolver.Resolve(policySet1)).ThenReturn(localPolicySetPath1, nil)
		When(mockResolver.Resolve(policySet2)).ThenReturn(localPolicySetPath2, nil)

		When(mockExec.CombinedOutput(expectedArgs, envs, workdir)).ThenReturn("", errors.New("err"))

		_, err := subject.Run(ctx, executablePath, envs, workdir)

		Assert(t, err != nil, "error is expected")

	})
}

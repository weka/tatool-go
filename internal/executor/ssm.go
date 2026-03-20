package executor

import (
	"context"
	"encoding/base64"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

const ssmDocument = "AWS-RunShellScript"
const ssmPollInterval = 2 * time.Second
const ssmExecTimeout = 10 * time.Minute

// SSMExecutor implements Executor using AWS Systems Manager Run Command.
type SSMExecutor struct {
	client  *ssm.Client
	scripts fs.FS
	mu      sync.RWMutex
}

// NewSSMExecutor creates an SSM executor using the AWS default credential chain.
// Region and profile are optional — if empty, the SDK falls back to environment
// variables and ~/.aws/config.
func NewSSMExecutor(region, profile string) (*SSMExecutor, error) {
	// WithEC2IMDSRegion enables region lookup from the instance metadata service
	// (IMDS) — not enabled by default in SDK v2. Required when running on EC2
	// without an explicit region in env vars or ~/.aws/config.
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithEC2IMDSRegion(),
	}
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	if profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	return &SSMExecutor{client: ssm.NewFromConfig(cfg)}, nil
}

// CopyScripts stashes the script filesystem for inline delivery in Exec.
// No remote action is taken here; scripts are written inline per-Exec call.
func (e *SSMExecutor) CopyScripts(_ context.Context, _ string, scripts fs.FS) error {
	e.mu.Lock()
	e.scripts = scripts
	e.mu.Unlock()
	return nil
}

// Exec writes a single script inline to the instance via SSM Run Command and
// returns the result. The script content is base64-encoded to avoid any shell
// quoting issues and decoded on the instance before execution.
func (e *SSMExecutor) Exec(ctx context.Context, instanceID string, scriptPath string, _ bool) (ExecResult, error) {
	e.mu.RLock()
	scripts := e.scripts
	e.mu.RUnlock()

	if scripts == nil {
		return ExecResult{}, fmt.Errorf("CopyScripts must be called before Exec")
	}

	data, err := fs.ReadFile(scripts, scriptPath)
	if err != nil {
		return ExecResult{}, fmt.Errorf("reading script %s: %w", scriptPath, err)
	}

	remotePath := remoteScriptDir + "/" + scriptPath
	encoded := base64.StdEncoding.EncodeToString(data)

	// Write the script via base64 decode (avoids all shell quoting issues),
	// then execute it. mkdir is idempotent so safe to repeat across scripts.
	commands := []string{
		"mkdir -p " + remoteScriptDir,
		fmt.Sprintf("echo %s | base64 -d > %s", encoded, remotePath),
		fmt.Sprintf("chmod +x %s", remotePath),
		remotePath,
	}

	return e.sendAndWait(ctx, instanceID, commands)
}

// Cleanup removes the deployed scripts directory from the instance.
func (e *SSMExecutor) Cleanup(ctx context.Context, instanceID string) error {
	_, err := e.sendAndWait(ctx, instanceID, []string{"rm -rf " + remoteScriptDir})
	return err
}

// FetchDiagnostics is not supported in SSM mode v1.
// Diagnostics remain on the instance at /tmp/diagnostics*.
func (e *SSMExecutor) FetchDiagnostics(_ context.Context, instanceID string, _ string) error {
	fmt.Printf("Note: diagnostics for %s remain on the instance at /tmp/diagnostics*\n", instanceID)
	return nil
}

// Close is a no-op for SSM (connections are per-request).
func (e *SSMExecutor) Close() error { return nil }

// sendAndWait sends an SSM Run Command to one instance and polls until
// it reaches a terminal state, then returns the result.
func (e *SSMExecutor) sendAndWait(ctx context.Context, instanceID string, commands []string) (ExecResult, error) {
	out, err := e.client.SendCommand(ctx, &ssm.SendCommandInput{
		InstanceIds:  []string{instanceID},
		DocumentName: aws.String(ssmDocument),
		Parameters: map[string][]string{
			"commands":         commands,
			"executionTimeout": {"600"},
		},
	})
	if err != nil {
		return ExecResult{}, fmt.Errorf("SSM SendCommand to %s: %w", instanceID, err)
	}

	commandID := aws.ToString(out.Command.CommandId)
	return e.pollResult(ctx, commandID, instanceID)
}

// pollResult polls GetCommandInvocation until the command reaches a terminal state.
func (e *SSMExecutor) pollResult(ctx context.Context, commandID, instanceID string) (ExecResult, error) {
	deadline := time.Now().Add(ssmExecTimeout)

	for {
		if time.Now().After(deadline) {
			return ExecResult{Status: StatusFail}, fmt.Errorf("SSM command %s timed out on %s", commandID, instanceID)
		}

		select {
		case <-ctx.Done():
			return ExecResult{}, ctx.Err()
		case <-time.After(ssmPollInterval):
		}

		inv, err := e.client.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{
			CommandId:  aws.String(commandID),
			InstanceId: aws.String(instanceID),
		})
		if err != nil {
			// Invocation record may not be visible immediately after SendCommand.
			continue
		}

		switch inv.Status {
		case types.CommandInvocationStatusPending,
			types.CommandInvocationStatusInProgress,
			types.CommandInvocationStatusDelayed:
			continue
		}

		// Terminal state reached.
		exitCode := int(inv.ResponseCode)
		stdout := aws.ToString(inv.StandardOutputContent)
		stderr := aws.ToString(inv.StandardErrorContent)

		// Warn if SSM truncated the output (2500-char limit).
		if strings.Contains(aws.ToString(inv.StatusDetails), "output limit exceeded") {
			stderr += "\n[WARNING: SSM output was truncated at 2500 chars]"
		}

		// ResponseCode -1 means the SSM agent itself failed to run the command.
		if exitCode == -1 {
			return ExecResult{
				Stdout:   stdout,
				Stderr:   stderr,
				ExitCode: 1,
				Status:   StatusFail,
			}, fmt.Errorf("SSM agent error on %s: %s", instanceID, aws.ToString(inv.StatusDetails))
		}

		return ExecResult{
			Stdout:   stdout,
			Stderr:   stderr,
			ExitCode: exitCode,
			Status:   StatusFromExitCode(exitCode),
		}, nil
	}
}

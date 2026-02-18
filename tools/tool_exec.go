package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

var safeExecEnvVars = []string{
	"PATH",
	"HOME",
	"TERM",
	"LANG",
	"LC_ALL",
	"LC_CTYPE",
	"USER",
	"SHELL",
	"TMPDIR",
}

func applySafeExecEnv(cmd *exec.Cmd) {
	cmd.Env = []string{}
	for _, key := range safeExecEnvVars {
		if val, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+val)
		}
	}
}

func (r *Registry) exec(ctx context.Context, command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", errors.New("command is empty")
	}
	if msg := guardExecCommand(command, r.WorkspaceDir, r.RestrictToWorkspace); msg != "" {
		return msg, nil
	}
	timeout := r.ExecTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Use sh -lc for portability (pipes, redirects, etc.)
	cmd := exec.CommandContext(cctx, "sh", "-lc", command)
	cmd.Dir = r.WorkspaceDir
	applySafeExecEnv(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	out := truncate(stdout.String(), 64<<10)
	serr := truncate(stderr.String(), 64<<10)
	exit := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode()
		} else {
			exit = -1
		}
	}
	res := fmt.Sprintf("exit=%d\n", exit)
	if out != "" {
		res += "stdout:\n" + out + "\n"
	}
	if serr != "" {
		res += "stderr:\n" + serr + "\n"
	}
	if err != nil && cctx.Err() == context.DeadlineExceeded {
		res += "error: timeout\n"
		return res, nil
	}
	// Return output even if non-zero; the model can decide next step.
	return strings.TrimRight(res, "\n"), nil
}

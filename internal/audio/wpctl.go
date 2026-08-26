package audio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Kind string

const (
	Sink   Kind = "sink"
	Source Kind = "source"
)

type CommandResult struct {
	Stdout []byte
	Stderr []byte
}

type CommandRunner interface {
	Run(context.Context, ...string) (CommandResult, error)
}

type ExecRunner struct {
	Path string
}

func (runner ExecRunner) Run(ctx context.Context, args ...string) (CommandResult, error) {
	command := exec.CommandContext(ctx, runner.Path, args...)
	command.Env = localeIndependentEnvironment(os.Environ())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, err
}

func localeIndependentEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment)+2)
	for _, variable := range environment {
		if strings.HasPrefix(variable, "LANG=") || strings.HasPrefix(variable, "LC_ALL=") {
			continue
		}
		result = append(result, variable)
	}
	return append(result, "LANG=C", "LC_ALL=C")
}

type WPCTL struct {
	runner  CommandRunner
	timeout time.Duration
}

func NewWPCTL(runner CommandRunner, timeout time.Duration) *WPCTL {
	return &WPCTL{runner: runner, timeout: timeout}
}

func (adapter *WPCTL) SetDefault(ctx context.Context, kind Kind, targetName string) error {
	plural, err := kind.plural()
	if err != nil {
		return err
	}

	result, err := adapter.run(ctx, "list", "audio", plural)
	if err != nil {
		return fmt.Errorf("list %ss for target %q: %w", kind, targetName, err)
	}
	id, err := resolveExactName(result.Stdout, targetName)
	if err != nil {
		return fmt.Errorf("resolve %s target %q: %w", kind, targetName, err)
	}

	if _, err := adapter.run(ctx, "set-default", id); err != nil {
		return fmt.Errorf("set default %s target %q: %w", kind, targetName, err)
	}
	return nil
}

func (adapter *WPCTL) run(ctx context.Context, args ...string) (CommandResult, error) {
	commandContext, cancel := context.WithTimeout(ctx, adapter.timeout)
	defer cancel()

	result, err := adapter.runner.Run(commandContext, args...)
	if err == nil {
		return result, nil
	}
	if errors.Is(commandContext.Err(), context.DeadlineExceeded) {
		return result, fmt.Errorf("wpctl %s timed out after %s", strings.Join(args, " "), adapter.timeout)
	}
	if errors.Is(commandContext.Err(), context.Canceled) {
		return result, context.Canceled
	}
	stderr := strings.TrimSpace(string(result.Stderr))
	if stderr != "" {
		return result, fmt.Errorf("wpctl %s: %w: %s", strings.Join(args, " "), err, stderr)
	}
	return result, fmt.Errorf("wpctl %s: %w", strings.Join(args, " "), err)
}

func (kind Kind) plural() (string, error) {
	switch kind {
	case Sink:
		return "sinks", nil
	case Source:
		return "sources", nil
	default:
		return "", fmt.Errorf("unsupported audio target kind %q", kind)
	}
}

func resolveExactName(output []byte, targetName string) (string, error) {
	var matches []string
	lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	for lineNumber, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			return "", fmt.Errorf("malformed wpctl list line %d", lineNumber+1)
		}
		id := strings.TrimSpace(fields[0])
		if _, err := strconv.ParseUint(id, 10, 64); err != nil {
			return "", fmt.Errorf("invalid object ID %q on wpctl list line %d", id, lineNumber+1)
		}
		if fields[1] == targetName {
			matches = append(matches, id)
		}
	}

	switch len(matches) {
	case 0:
		return "", errors.New("no exact node.name match")
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous node.name matched %d objects", len(matches))
	}
}

package audio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
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
	runner   CommandRunner
	timeout  time.Duration
	observer ResolverObserver
}

type ResolvedCandidate struct {
	Name     string
	Priority *int
}

type ResolverObserver interface {
	AutomaticTargetSelected(Kind, USBIdentity, string, string, []ResolvedCandidate)
}

func NewWPCTL(runner CommandRunner, timeout time.Duration) *WPCTL {
	return &WPCTL{runner: runner, timeout: timeout}
}

func (adapter *WPCTL) SetResolverObserver(observer ResolverObserver) {
	adapter.observer = observer
}

func (adapter *WPCTL) SetDefault(ctx context.Context, kind Kind, target Target) error {
	id, err := adapter.resolveTarget(ctx, kind, target)
	if err != nil {
		return err
	}
	if _, err := adapter.run(ctx, "set-default", id); err != nil {
		return fmt.Errorf("set default %s target %q: %w", kind, target.Name, err)
	}
	return nil
}

func (adapter *WPCTL) SetVolume(ctx context.Context, target Target, volume uint8) error {
	if volume > 15 {
		return fmt.Errorf("volume %d is outside 0..15", volume)
	}
	id, err := adapter.resolveTarget(ctx, Sink, target)
	if err != nil {
		return err
	}
	ratio := strconv.FormatFloat(float64(volume)/15, 'f', -1, 64)
	if _, err := adapter.run(ctx, "set-volume", id, ratio); err != nil {
		return fmt.Errorf("set sink target %q volume to %s: %w", target.Name, ratio, err)
	}
	return nil
}

func (adapter *WPCTL) SetMute(ctx context.Context, target Target, muted bool) error {
	id, err := adapter.resolveTarget(ctx, Source, target)
	if err != nil {
		return err
	}
	value := "0"
	if muted {
		value = "1"
	}
	if _, err := adapter.run(ctx, "set-mute", id, value); err != nil {
		return fmt.Errorf("set source target %q mute to %s: %w", target.Name, value, err)
	}
	return nil
}

func (adapter *WPCTL) resolveTarget(ctx context.Context, kind Kind, target Target) (string, error) {
	plural, err := kind.plural()
	if err != nil {
		return "", err
	}

	var id string
	if target.Name != "" {
		result, err := adapter.run(ctx, "list", "audio", plural)
		if err != nil {
			return "", fmt.Errorf("list %ss for target %q: %w", kind, target.Name, err)
		}
		id, err = resolveExactName(result.Stdout, target.Name)
		if err != nil {
			return "", fmt.Errorf("resolve %s target %q: %w", kind, target.Name, err)
		}
	} else {
		var selectedName string
		var deviceID string
		var candidates []ResolvedCandidate
		id, selectedName, deviceID, candidates, err = adapter.resolveAutomatic(ctx, kind, target.USB)
		if err != nil {
			return "", fmt.Errorf("resolve automatic %s target: %w", kind, err)
		}
		if adapter.observer != nil {
			adapter.observer.AutomaticTargetSelected(
				kind, target.USB, deviceID, selectedName, candidates,
			)
		}
	}

	return id, nil
}

func (adapter *WPCTL) resolveAutomatic(
	ctx context.Context,
	kind Kind,
	usb USBIdentity,
) (string, string, string, []ResolvedCandidate, error) {
	if usb.Syspath == "" {
		return "", "", "", nil, errors.New("selected adapter has no USB parent syspath")
	}
	deviceResult, err := adapter.run(ctx, "list", "audio", "devices")
	if err != nil {
		return "", "", "", nil, fmt.Errorf("list audio devices: %w", err)
	}
	deviceIDs, err := parseListIDs(deviceResult.Stdout)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("parse audio devices: %w", err)
	}
	var matches []string
	for _, deviceID := range deviceIDs {
		properties, inspectErr := adapter.inspect(ctx, deviceID)
		if inspectErr != nil {
			return "", "", "", nil, inspectErr
		}
		path := properties["device.sysfs.path"]
		if !belongsToUSB(path, usb.Syspath) {
			continue
		}
		if serial := properties["device.serial"]; serial != "" && usb.Serial != "" &&
			!serialsMatch(serial, usb.Serial) {
			return "", "", "", nil, fmt.Errorf(
				"audio device %s serial %q does not match selected USB serial %q",
				deviceID, serial, usb.Serial,
			)
		}
		matches = append(matches, deviceID)
	}
	if len(matches) == 0 {
		return "", "", "", nil, errors.New("no audio device belongs to selected USB adapter")
	}
	if len(matches) != 1 {
		return "", "", "", nil, fmt.Errorf(
			"selected USB adapter matched %d audio devices", len(matches),
		)
	}
	deviceID := matches[0]
	plural, _ := kind.plural()
	nodeResult, err := adapter.run(ctx, "list", "audio", plural)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("list audio %s: %w", plural, err)
	}
	nodeIDs, err := parseListIDs(nodeResult.Stdout)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("parse audio %s: %w", plural, err)
	}
	type eligibleNode struct {
		id string
		ResolvedCandidate
	}
	var eligible []eligibleNode
	for _, nodeID := range nodeIDs {
		properties, inspectErr := adapter.inspect(ctx, nodeID)
		if inspectErr != nil {
			return "", "", "", nil, inspectErr
		}
		if properties["device.id"] != deviceID {
			continue
		}
		name := properties["node.name"]
		if name == "" {
			return "", "", "", nil, fmt.Errorf("audio node %s has no node.name", nodeID)
		}
		var priority *int
		if text, present := properties["priority.session"]; present {
			value, parseErr := strconv.Atoi(text)
			if parseErr != nil {
				return "", "", "", nil, fmt.Errorf(
					"audio node %s has invalid priority.session %q", nodeID, text,
				)
			}
			priority = &value
		}
		eligible = append(eligible, eligibleNode{
			id:                nodeID,
			ResolvedCandidate: ResolvedCandidate{Name: name, Priority: priority},
		})
	}
	if len(eligible) == 0 {
		return "", "", "", nil, fmt.Errorf("audio device %s has no eligible %s nodes", deviceID, kind)
	}
	sort.Slice(eligible, func(left, right int) bool {
		leftPriority := eligible[left].Priority
		rightPriority := eligible[right].Priority
		if leftPriority == nil && rightPriority != nil {
			return false
		}
		if leftPriority != nil && rightPriority == nil {
			return true
		}
		if leftPriority != nil && *leftPriority != *rightPriority {
			return *leftPriority > *rightPriority
		}
		return eligible[left].Name < eligible[right].Name
	})
	candidates := make([]ResolvedCandidate, len(eligible))
	for index := range eligible {
		candidates[index] = eligible[index].ResolvedCandidate
	}
	return eligible[0].id, eligible[0].Name, deviceID, candidates, nil
}

func (adapter *WPCTL) inspect(ctx context.Context, id string) (map[string]string, error) {
	result, err := adapter.run(ctx, "inspect", id)
	if err != nil {
		return nil, fmt.Errorf("inspect object %s: %w", id, err)
	}
	properties, err := parseInspectProperties(result.Stdout)
	if err != nil {
		return nil, fmt.Errorf("inspect object %s: %w", id, err)
	}
	return properties, nil
}

func parseListIDs(output []byte) ([]string, error) {
	lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	ids := make([]string, 0, len(lines))
	for lineNumber, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			return nil, fmt.Errorf("malformed wpctl list line %d", lineNumber+1)
		}
		id := strings.TrimSpace(fields[0])
		if _, err := strconv.ParseUint(id, 10, 64); err != nil {
			return nil, fmt.Errorf("invalid object ID %q on wpctl list line %d", id, lineNumber+1)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func parseInspectProperties(output []byte) (map[string]string, error) {
	properties := make(map[string]string)
	for lineNumber, line := range strings.Split(string(output), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), " = ")
		if !found {
			continue
		}
		key = strings.TrimSpace(strings.TrimPrefix(key, "*"))
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "\"") {
			decoded, err := strconv.Unquote(value)
			if err != nil {
				if len(value) < 2 || !strings.HasSuffix(value, "\"") {
					return nil, fmt.Errorf("invalid quoted value on inspect line %d", lineNumber+1)
				}
				decoded = value[1 : len(value)-1]
			}
			value = decoded
		}
		properties[key] = value
	}
	return properties, nil
}

func belongsToUSB(path, usbSyspath string) bool {
	path = normalizeSysfsPath(path)
	usbSyspath = normalizeSysfsPath(usbSyspath)
	return path == usbSyspath || strings.HasPrefix(path, usbSyspath+"/") ||
		strings.HasPrefix(path, usbSyspath+":")
}

func normalizeSysfsPath(path string) string {
	if strings.HasPrefix(path, "/sys/") {
		return strings.TrimPrefix(path, "/sys")
	}
	return path
}

func serialsMatch(audioSerial, usbSerial string) bool {
	return audioSerial == usbSerial || strings.HasSuffix(audioSerial, "_"+usbSerial)
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

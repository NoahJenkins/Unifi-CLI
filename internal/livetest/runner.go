package livetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type DataShape uint8

const (
	ArrayData DataShape = iota
	ObjectData
)

type Command struct {
	Name     string
	Resource string
	Action   string
	Args     []string
	Shape    DataShape
	Optional bool
	GetFrom  *GetSpec
}

type GetSpec struct {
	Command         Command
	IDField         string
	PortDeviceField string
	PortIndexField  string
}

type Meta struct {
	Count *int `json:"count"`
}

type Envelope struct {
	OK       bool            `json:"ok"`
	Resource string          `json:"resource"`
	Action   string          `json:"action"`
	Data     json.RawMessage `json:"data"`
	Meta     Meta            `json:"meta"`
}

type Executor interface {
	Run(context.Context, string, ...string) ([]byte, []byte, int, error)
}

type OSExecutor struct{}

func (OSExecutor) Run(ctx context.Context, binary string, args ...string) ([]byte, []byte, int, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	stdout, err := cmd.Output()
	if err == nil {
		return stdout, nil, 0, nil
	}

	exitCode := 1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
		return stdout, exitErr.Stderr, exitCode, err
	}
	return stdout, nil, exitCode, err
}

func ReadOnlyCommands() []Command {
	return []Command{
		objectCommand("auth status", "auth", "status", "auth", "status"),
		objectCommand("config path", "config", "path", "config", "path"),
		objectCommand("config show", "config", "show", "config", "show"),
		listWithGet("site", "site", "id"),
		listWithGet("device", "device", "id"),
		listWithGet("client", "client", "id"),
		listWithGet("network", "network", "id"),
		listWithGet("wlan", "wlan", "id"),
		portListCommand(),
		optionalListWithGet("firewall", "firewall", "id"),
		optionalListWithGet("dns", "dns", "id"),
		listCommand("dns resolvers list", "dns", "resolvers list", "dns", "resolvers", "list"),
		objectCommand("system health", "system", "health", "system", "health"),
		listCommand("system events", "system", "events", "system", "events"),
		listCommand("system alerts", "system", "alerts", "system", "alerts"),
	}
}

func objectCommand(name, resource, action string, args ...string) Command {
	return Command{Name: name, Resource: resource, Action: action, Args: args, Shape: ObjectData}
}

func listCommand(name, resource, action string, args ...string) Command {
	return Command{Name: name, Resource: resource, Action: action, Args: args, Shape: ArrayData}
}

func listWithGet(resource, name, idField string) Command {
	return Command{
		Name: resource + " list", Resource: resource, Action: "list", Args: []string{resource, "list"}, Shape: ArrayData,
		GetFrom: &GetSpec{Command: objectCommand(resource+" get", resource, "get", resource, "get"), IDField: idField},
	}
}

func optionalListWithGet(resource, name, idField string) Command {
	command := listWithGet(resource, name, idField)
	command.Optional = true
	return command
}

func portListCommand() Command {
	return Command{
		Name: "port list", Resource: "port", Action: "list", Args: []string{"port", "list"}, Shape: ArrayData,
		GetFrom: &GetSpec{
			Command:         objectCommand("port get", "port", "get", "port", "get"),
			PortDeviceField: "device_id",
			PortIndexField:  "port_idx",
		},
	}
}

func Validate(command Command, stdout []byte) (Envelope, []map[string]any, error) {
	var env Envelope
	if err := json.Unmarshal(stdout, &env); err != nil {
		return Envelope{}, nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if !env.OK {
		return Envelope{}, nil, errors.New("envelope ok is false")
	}
	if env.Resource != command.Resource || env.Action != command.Action {
		return Envelope{}, nil, fmt.Errorf("got %s %s, want %s %s", env.Resource, env.Action, command.Resource, command.Action)
	}
	if command.Shape == ObjectData {
		var object map[string]any
		if err := json.Unmarshal(env.Data, &object); err != nil || object == nil {
			return Envelope{}, nil, errors.New("expected object data")
		}
		return env, nil, nil
	}

	var items []map[string]any
	if err := json.Unmarshal(env.Data, &items); err != nil {
		return Envelope{}, nil, fmt.Errorf("expected array data: %w", err)
	}
	if env.Meta.Count != nil && *env.Meta.Count != len(items) {
		return Envelope{}, nil, fmt.Errorf("meta count %d does not match data length %d", *env.Meta.Count, len(items))
	}
	return env, items, nil
}

type Status string

const (
	Pass          Status = "pass"
	NotConfigured Status = "not_configured"
	Fail          Status = "fail"
)

type Result struct {
	Command    string `json:"command"`
	Status     Status `json:"status"`
	Summary    string `json:"summary"`
	DurationMS int64  `json:"duration_ms"`
}

type Report struct {
	StartedAt time.Time `json:"started_at"`
	Results   []Result  `json:"results"`
}

type Runner struct {
	Binary   string
	Executor Executor
	Commands []Command
	Now      func() time.Time
}

func (r Runner) Run(ctx context.Context) (Report, error) {
	now := r.Now
	if now == nil {
		now = time.Now
	}
	commands := r.Commands
	if len(commands) == 0 {
		commands = ReadOnlyCommands()
	}
	executor := r.Executor
	if executor == nil {
		executor = OSExecutor{}
	}

	report := Report{StartedAt: now(), Results: make([]Result, 0, len(commands)*2)}
	var failures []error
	for _, command := range commands {
		result, items, ok := r.runCommand(ctx, now, executor, command)
		report.Results = append(report.Results, result)
		if !ok {
			failures = append(failures, fmt.Errorf("%s failed", command.Name))
			continue
		}
		if command.GetFrom == nil {
			continue
		}
		if len(items) == 0 {
			if command.Optional {
				report.Results[len(report.Results)-1].Status = NotConfigured
				report.Results[len(report.Results)-1].Summary = "not configured"
			}
			continue
		}

		getCommand, err := deriveGetCommand(*command.GetFrom, items[0])
		if err != nil {
			report.Results = append(report.Results, Result{Command: command.GetFrom.Command.Name, Status: Fail, Summary: "missing lookup fields"})
			failures = append(failures, fmt.Errorf("%s failed", command.GetFrom.Command.Name))
			continue
		}
		getResult, _, getOK := r.runCommand(ctx, now, executor, getCommand)
		report.Results = append(report.Results, getResult)
		if !getOK {
			failures = append(failures, fmt.Errorf("%s failed", getCommand.Name))
		}
	}
	return report, errors.Join(failures...)
}

func (r Runner) runCommand(ctx context.Context, now func() time.Time, executor Executor, command Command) (result Result, items []map[string]any, ok bool) {
	started := now()
	result = Result{Command: command.Name, Status: Fail}
	defer func() { result.DurationMS = now().Sub(started).Milliseconds() }()

	if !commandIsSafe(command) {
		result.Summary = "invalid command configuration"
		return result, nil, false
	}
	args := append(append([]string{}, command.Args...), "--json", "--no-session-write")
	stdout, _, exitCode, err := executor.Run(ctx, r.Binary, args...)
	if err != nil || exitCode != 0 {
		result.Summary = exitSummary(exitCode, err)
		return result, nil, false
	}
	_, items, err = Validate(command, stdout)
	if err != nil {
		result.Summary = "invalid response"
		return result, nil, false
	}
	result.Status = Pass
	result.Summary = "validated"
	return result, items, true
}

func deriveGetCommand(spec GetSpec, item map[string]any) (Command, error) {
	command := spec.Command
	if spec.IDField != "" {
		id, ok := item[spec.IDField].(string)
		if !ok || id == "" {
			return Command{}, errors.New("missing id")
		}
		command.Args = append(append([]string{}, command.Args...), id)
		return command, nil
	}

	device, ok := item[spec.PortDeviceField].(string)
	if !ok || device == "" {
		return Command{}, errors.New("missing port device")
	}
	index, ok := item[spec.PortIndexField].(float64)
	if !ok || math.IsNaN(index) || math.IsInf(index, 0) || index != math.Trunc(index) {
		return Command{}, errors.New("missing port index")
	}
	command.Args = append(append([]string{}, command.Args...), device, strconv.FormatInt(int64(index), 10))
	return command, nil
}

func commandIsSafe(command Command) bool {
	forbidden := map[string]bool{
		"create": true, "update": true, "delete": true, "rename": true,
		"restart": true, "locate": true, "upgrade": true, "adopt": true,
		"forget": true, "reconnect": true, "block": true, "unblock": true,
		"enable": true, "disable": true, "reorder": true, "set": true,
		"--yes": true, "--dry-run": true, "--raw": true, "--json": true,
	}
	for _, token := range append(append([]string{}, command.Args...), strings.Fields(command.Name)...) {
		if forbidden[token] {
			return false
		}
	}
	return true
}

func exitSummary(exitCode int, err error) string {
	if exitCode != 0 {
		return fmt.Sprintf("command exited with status %d", exitCode)
	}
	if err != nil {
		return "process execution failed"
	}
	return "check failed"
}

func WriteReport(dir string, report Report) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	startedAt := report.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	path := filepath.Join(dir, "read-only-"+startedAt.UTC().Format("20060102T150405.000000000Z")+".json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	defer file.Close()

	type outputResult struct {
		Command    string `json:"command"`
		Status     Status `json:"status"`
		Summary    string `json:"summary"`
		DurationMS int64  `json:"duration_ms"`
	}
	output := struct {
		StartedAt time.Time      `json:"started_at"`
		Results   []outputResult `json:"results"`
	}{StartedAt: startedAt.UTC(), Results: make([]outputResult, 0, len(report.Results))}
	for _, result := range report.Results {
		output.Results = append(output.Results, outputResult{
			Command: result.Command, Status: result.Status, Summary: reportSummary(result.Status), DurationMS: result.DurationMS,
		})
	}
	if err := json.NewEncoder(file).Encode(output); err != nil {
		return "", err
	}
	return path, nil
}

func reportSummary(status Status) string {
	switch status {
	case Pass:
		return "validated"
	case NotConfigured:
		return "not configured"
	default:
		return "check failed"
	}
}

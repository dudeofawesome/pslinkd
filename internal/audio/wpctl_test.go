package audio

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

const sinkFixture = "31\talsa_output.pci-speaker\tAudio/Sink\n" +
	"47\talsa_output.usb-Sony\tAudio/Sink\t*\n"

type commandCall struct {
	args []string
}

type commandResponse struct {
	result CommandResult
	err    error
	block  bool
}

type fakeCommandRunner struct {
	calls     []commandCall
	responses []commandResponse
}

func (runner *fakeCommandRunner) Run(ctx context.Context, args ...string) (CommandResult, error) {
	runner.calls = append(runner.calls, commandCall{args: append([]string(nil), args...)})
	response := runner.responses[0]
	runner.responses = runner.responses[1:]
	if response.block {
		<-ctx.Done()
		return CommandResult{}, ctx.Err()
	}
	return response.result, response.err
}

func TestWPCTLResolvesExactNameAndUsesCurrentID(t *testing.T) {
	runner := &fakeCommandRunner{responses: []commandResponse{
		{result: CommandResult{Stdout: []byte(sinkFixture)}},
		{},
	}}
	adapter := NewWPCTL(runner, time.Second)

	if err := adapter.SetDefault(context.Background(), Sink, "alsa_output.usb-Sony"); err != nil {
		t.Fatal(err)
	}
	want := []commandCall{
		{args: []string{"list", "audio", "sinks"}},
		{args: []string{"set-default", "47"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestWPCTLRefreshesEphemeralIDForEveryAction(t *testing.T) {
	runner := &fakeCommandRunner{responses: []commandResponse{
		{result: CommandResult{Stdout: []byte("47\tspeaker\tAudio/Sink\n")}},
		{},
		{result: CommandResult{Stdout: []byte("91\tspeaker\tAudio/Sink\n")}},
		{},
	}}
	adapter := NewWPCTL(runner, time.Second)
	if err := adapter.SetDefault(context.Background(), Sink, "speaker"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.SetDefault(context.Background(), Sink, "speaker"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"list", "audio", "sinks"},
		{"set-default", "47"},
		{"list", "audio", "sinks"},
		{"set-default", "91"},
	}
	for index, call := range runner.calls {
		if !reflect.DeepEqual(call.args, want[index]) {
			t.Fatalf("call %d = %#v, want %#v", index, call.args, want[index])
		}
	}
}

func TestWPCTLResolvesSourceUsingSourceList(t *testing.T) {
	runner := &fakeCommandRunner{responses: []commandResponse{
		{result: CommandResult{Stdout: []byte("62\talsa_input.usb-Sony\tAudio/Source\n")}},
		{},
	}}
	adapter := NewWPCTL(runner, time.Second)
	if err := adapter.SetDefault(context.Background(), Source, "alsa_input.usb-Sony"); err != nil {
		t.Fatal(err)
	}
	if got := runner.calls[0].args; !reflect.DeepEqual(got, []string{"list", "audio", "sources"}) {
		t.Fatalf("list args = %#v", got)
	}
}

func TestWPCTLRejectsMissingAndAmbiguousTargets(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{name: "missing", output: sinkFixture, want: "no exact node.name match"},
		{
			name: "ambiguous",
			output: "47\tduplicate\tAudio/Sink\n" +
				"92\tduplicate\tAudio/Sink\n",
			want: "ambiguous node.name matched 2 objects",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeCommandRunner{responses: []commandResponse{{
				result: CommandResult{Stdout: []byte(test.output)},
			}}}
			err := NewWPCTL(runner, time.Second).SetDefault(context.Background(), Sink, "duplicate")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
			if len(runner.calls) != 1 {
				t.Fatalf("calls = %#v", runner.calls)
			}
		})
	}
}

func TestWPCTLReportsSubprocessStderr(t *testing.T) {
	runner := &fakeCommandRunner{responses: []commandResponse{{
		result: CommandResult{Stderr: []byte("WirePlumber is unavailable\n")},
		err:    errors.New("exit status 3"),
	}}}
	err := NewWPCTL(runner, time.Second).SetDefault(context.Background(), Sink, "speaker")
	if err == nil || !strings.Contains(err.Error(), "WirePlumber is unavailable") {
		t.Fatalf("error = %v", err)
	}
}

func TestWPCTLTimesOutEverySubprocess(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		runner := &fakeCommandRunner{responses: []commandResponse{{block: true}}}
		err := NewWPCTL(runner, time.Millisecond).SetDefault(context.Background(), Sink, "speaker")
		if err == nil || !strings.Contains(err.Error(), "timed out after 1ms") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("set default", func(t *testing.T) {
		runner := &fakeCommandRunner{responses: []commandResponse{
			{result: CommandResult{Stdout: []byte("47\tspeaker\tAudio/Sink\n")}},
			{block: true},
		}}
		err := NewWPCTL(runner, time.Millisecond).SetDefault(context.Background(), Sink, "speaker")
		if err == nil || !strings.Contains(err.Error(), "timed out after 1ms") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestLocaleIndependentEnvironment(t *testing.T) {
	got := localeIndependentEnvironment([]string{"PATH=/bin", "LANG=de_DE", "LC_ALL=fr_FR"})
	want := []string{"PATH=/bin", "LANG=C", "LC_ALL=C"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment = %#v, want %#v", got, want)
	}
}

func TestResolveExactNameRejectsMalformedFixture(t *testing.T) {
	_, err := resolveExactName([]byte("not-machine-readable\n"), "speaker")
	if err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("error = %v", err)
	}
}

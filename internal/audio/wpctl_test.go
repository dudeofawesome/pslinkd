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

type resolverEvent struct {
	kind       Kind
	usb        USBIdentity
	deviceID   string
	selected   string
	candidates []ResolvedCandidate
}

type fakeResolverObserver struct {
	events []resolverEvent
}

func (observer *fakeResolverObserver) AutomaticTargetSelected(
	kind Kind,
	usb USBIdentity,
	deviceID string,
	selected string,
	candidates []ResolvedCandidate,
) {
	observer.events = append(observer.events, resolverEvent{
		kind: kind, usb: usb, deviceID: deviceID, selected: selected,
		candidates: candidates,
	})
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

	if err := adapter.SetDefault(context.Background(), Sink, Target{Name: "alsa_output.usb-Sony"}); err != nil {
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
	if err := adapter.SetDefault(context.Background(), Sink, Target{Name: "speaker"}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.SetDefault(context.Background(), Sink, Target{Name: "speaker"}); err != nil {
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

func TestWPCTLAutomaticallyAssociatesUSBAndSelectsHighestPriorityNode(t *testing.T) {
	usb := USBIdentity{Syspath: "/sys/devices/usb1/1-2", Serial: "selected-serial"}
	runner := &fakeCommandRunner{responses: []commandResponse{
		{result: CommandResult{Stdout: []byte(
			"10\tother-adapter\tAudio/Device\n20\tselected-adapter\tAudio/Device\n",
		)}},
		{result: CommandResult{Stdout: []byte(
			"device.sysfs.path = \"/sys/devices/usb1/1-3/1-3:1.0/sound/card1\"\n" +
				"device.vendor.id = \"054c\"\ndevice.product.id = \"0ecc\"\n",
		)}},
		{result: CommandResult{Stdout: []byte(
			"device.sysfs.path = \"/sys/devices/usb1/1-2/1-2:1.0/sound/card2\"\n" +
				"device.serial = \"selected-serial\"\n",
		)}},
		{result: CommandResult{Stdout: []byte(
			"31\tmissing-priority\tAudio/Sink\n" +
				"32\tzeta\tAudio/Sink\n33\talpha\tAudio/Sink\n34\tother-device\tAudio/Sink\n",
		)}},
		{result: CommandResult{Stdout: []byte(
			"device.id = 20\nnode.name = \"missing-priority\"\n",
		)}},
		{result: CommandResult{Stdout: []byte(
			"device.id = 20\nnode.name = \"zeta\"\npriority.session = 100\n",
		)}},
		{result: CommandResult{Stdout: []byte(
			"device.id = 20\nnode.name = \"alpha\"\npriority.session = 100\n",
		)}},
		{result: CommandResult{Stdout: []byte(
			"device.id = 10\nnode.name = \"other\"\npriority.session = 999\n",
		)}},
		{},
	}}
	observer := &fakeResolverObserver{}
	adapter := NewWPCTL(runner, time.Second)
	adapter.SetResolverObserver(observer)
	if err := adapter.SetDefault(context.Background(), Sink, Target{USB: usb}); err != nil {
		t.Fatal(err)
	}
	if got := runner.calls[len(runner.calls)-1].args; !reflect.DeepEqual(got, []string{"set-default", "33"}) {
		t.Fatalf("set-default args = %#v", got)
	}
	if len(observer.events) != 1 || observer.events[0].selected != "alpha" ||
		observer.events[0].deviceID != "20" || len(observer.events[0].candidates) != 3 {
		t.Fatalf("resolver events = %#v", observer.events)
	}
	priorities := observer.events[0].candidates
	if priorities[0].Priority == nil || *priorities[0].Priority != 100 ||
		priorities[2].Priority != nil {
		t.Fatalf("ordered candidates = %#v", priorities)
	}
}

func TestWPCTLAutomaticSourceUsesSourceNodes(t *testing.T) {
	runner := &fakeCommandRunner{responses: []commandResponse{
		{result: CommandResult{Stdout: []byte("20\tadapter\tAudio/Device\n")}},
		{result: CommandResult{Stdout: []byte("device.sysfs.path = \"/sys/usb/1-2\"\n")}},
		{result: CommandResult{Stdout: []byte("62\tmic\tAudio/Source\n")}},
		{result: CommandResult{Stdout: []byte(
			"device.id = 20\nnode.name = \"headset-mic\"\npriority.session = 50\n",
		)}},
		{},
	}}
	err := NewWPCTL(runner, time.Second).SetDefault(
		context.Background(), Source, Target{USB: USBIdentity{Syspath: "/sys/usb/1-2"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := runner.calls[2].args; !reflect.DeepEqual(got, []string{"list", "audio", "sources"}) {
		t.Fatalf("source list args = %#v", got)
	}
}

func TestWPCTLRefreshesAutomaticDeviceAndNodeIDsForEveryAction(t *testing.T) {
	responses := append(
		automaticResponses("20", "31", "headset"),
		automaticResponses("50", "91", "headset")...,
	)
	runner := &fakeCommandRunner{responses: responses}
	adapter := NewWPCTL(runner, time.Second)
	target := Target{USB: USBIdentity{Syspath: "/sys/usb/selected"}}
	if err := adapter.SetDefault(context.Background(), Sink, target); err != nil {
		t.Fatal(err)
	}
	if err := adapter.SetDefault(context.Background(), Sink, target); err != nil {
		t.Fatal(err)
	}
	var defaults [][]string
	for _, call := range runner.calls {
		if len(call.args) > 0 && call.args[0] == "set-default" {
			defaults = append(defaults, call.args)
		}
	}
	want := [][]string{{"set-default", "31"}, {"set-default", "91"}}
	if !reflect.DeepEqual(defaults, want) {
		t.Fatalf("set-default calls = %#v, want %#v", defaults, want)
	}
}

func automaticResponses(deviceID, nodeID, nodeName string) []commandResponse {
	return []commandResponse{
		{result: CommandResult{Stdout: []byte(deviceID + "\tadapter\tAudio/Device\n")}},
		{result: CommandResult{Stdout: []byte(
			"device.sysfs.path = \"/sys/usb/selected\"\n",
		)}},
		{result: CommandResult{Stdout: []byte(nodeID + "\t" + nodeName + "\tAudio/Sink\n")}},
		{result: CommandResult{Stdout: []byte(
			"device.id = " + deviceID + "\nnode.name = \"" + nodeName + "\"\n",
		)}},
		{},
	}
}

func TestWPCTLRejectsInvalidAutomaticDeviceAssociation(t *testing.T) {
	tests := []struct {
		name      string
		devices   string
		inspects  []string
		usb       USBIdentity
		wantError string
	}{
		{
			name: "malformed device list", devices: "not-machine-readable\n",
			usb:       USBIdentity{Syspath: "/sys/usb/selected"},
			wantError: "malformed wpctl list",
		},
		{
			name: "absent", devices: "10\tother\tAudio/Device\n",
			inspects:  []string{"device.sysfs.path = \"/sys/usb/elsewhere\"\n"},
			usb:       USBIdentity{Syspath: "/sys/usb/selected"},
			wantError: "no audio device belongs",
		},
		{
			name: "non-unique", devices: "10\ta\tAudio/Device\n11\tb\tAudio/Device\n",
			inspects: []string{
				"device.sysfs.path = \"/sys/usb/selected/a\"\n",
				"device.sysfs.path = \"/sys/usb/selected/b\"\n",
			},
			usb:       USBIdentity{Syspath: "/sys/usb/selected"},
			wantError: "matched 2 audio devices",
		},
		{
			name: "serial mismatch", devices: "10\ta\tAudio/Device\n",
			inspects: []string{
				"device.sysfs.path = \"/sys/usb/selected\"\ndevice.serial = \"wrong\"\n",
			},
			usb:       USBIdentity{Syspath: "/sys/usb/selected", Serial: "right"},
			wantError: "does not match selected USB serial",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			responses := []commandResponse{{result: CommandResult{Stdout: []byte(test.devices)}}}
			for _, inspect := range test.inspects {
				responses = append(responses, commandResponse{
					result: CommandResult{Stdout: []byte(inspect)},
				})
			}
			runner := &fakeCommandRunner{responses: responses}
			err := NewWPCTL(runner, time.Second).SetDefault(
				context.Background(), Sink, Target{USB: test.usb},
			)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}

func TestWPCTLRejectsMalformedAutomaticNodePriority(t *testing.T) {
	runner := &fakeCommandRunner{responses: []commandResponse{
		{result: CommandResult{Stdout: []byte("20\tadapter\tAudio/Device\n")}},
		{result: CommandResult{Stdout: []byte("device.sysfs.path = \"/sys/usb/a\"\n")}},
		{result: CommandResult{Stdout: []byte("31\tsink\tAudio/Sink\n")}},
		{result: CommandResult{Stdout: []byte(
			"device.id = 20\nnode.name = \"sink\"\npriority.session = high\n",
		)}},
	}}
	err := NewWPCTL(runner, time.Second).SetDefault(
		context.Background(), Sink, Target{USB: USBIdentity{Syspath: "/sys/usb/a"}},
	)
	if err == nil || !strings.Contains(err.Error(), "invalid priority.session") {
		t.Fatalf("error = %v", err)
	}
}

func TestWPCTLResolvesSourceUsingSourceList(t *testing.T) {
	runner := &fakeCommandRunner{responses: []commandResponse{
		{result: CommandResult{Stdout: []byte("62\talsa_input.usb-Sony\tAudio/Source\n")}},
		{},
	}}
	adapter := NewWPCTL(runner, time.Second)
	if err := adapter.SetDefault(context.Background(), Source, Target{Name: "alsa_input.usb-Sony"}); err != nil {
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
			err := NewWPCTL(runner, time.Second).SetDefault(
				context.Background(), Sink, Target{Name: "duplicate"},
			)
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
	err := NewWPCTL(runner, time.Second).SetDefault(
		context.Background(), Sink, Target{Name: "speaker"},
	)
	if err == nil || !strings.Contains(err.Error(), "WirePlumber is unavailable") {
		t.Fatalf("error = %v", err)
	}
}

func TestWPCTLTimesOutEverySubprocess(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		runner := &fakeCommandRunner{responses: []commandResponse{{block: true}}}
		err := NewWPCTL(runner, time.Millisecond).SetDefault(
			context.Background(), Sink, Target{Name: "speaker"},
		)
		if err == nil || !strings.Contains(err.Error(), "timed out after 1ms") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("set default", func(t *testing.T) {
		runner := &fakeCommandRunner{responses: []commandResponse{
			{result: CommandResult{Stdout: []byte("47\tspeaker\tAudio/Sink\n")}},
			{block: true},
		}}
		err := NewWPCTL(runner, time.Millisecond).SetDefault(
			context.Background(), Sink, Target{Name: "speaker"},
		)
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

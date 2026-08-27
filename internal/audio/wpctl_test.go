package audio

import (
	"context"
	"errors"
	"reflect"
	"strconv"
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
	usb := USBIdentity{
		Syspath: "/sys/devices/pci0000:00/0000:69:00.0/usb3/3-4",
		Serial:  "901c131a-6085-0492-06ec-d05027810150",
	}
	runner := &fakeCommandRunner{responses: []commandResponse{
		{result: CommandResult{Stdout: []byte(
			"10\tother-adapter\tAudio/Device\n20\tselected-adapter\tAudio/Device\n",
		)}},
		{result: CommandResult{Stdout: []byte(
			"device.sysfs.path = \"/devices/pci0000:00/0000:69:00.0/usb3/3-5/3-5:1.0/sound/card1\"\n" +
				"device.vendor.id = \"054c\"\ndevice.product.id = \"0ecc\"\n",
		)}},
		{result: CommandResult{Stdout: []byte(
			"device.sysfs.path = \"/devices/pci0000:00/0000:69:00.0/usb3/3-4/3-4:1.0/sound/card2\"\n" +
				"device.serial = \"Sony_Interactive_Entertainment_PlayStation_Link_Adapter_" +
				"901c131a-6085-0492-06ec-d05027810150\"\n",
		)}},
		{result: CommandResult{Stdout: []byte(
			"31\tmissing-priority\tAudio/Sink\n" +
				"32\tzeta\tAudio/Sink\n33\talpha\tAudio/Sink\n34\tother-device\tAudio/Sink\n",
		)}},
		{result: CommandResult{Stdout: []byte(
			"device.id = 20\n" +
				"iec958.codecs = \"[\"PCM\",\"DTS\",\"AC3\",\"EAC3\",\"TrueHD\",\"DTS-HD\"]\"\n" +
				"node.name = \"missing-priority\"\n",
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

func TestBelongsToUSBNormalizesSysfsMountPrefixAndPreservesBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		usbSyspath string
		want       bool
	}{
		{
			name:       "PipeWire omits sys mount prefix",
			path:       "/devices/pci0000:00/usb3/3-4/3-4:1.0/sound/card3",
			usbSyspath: "/sys/devices/pci0000:00/usb3/3-4",
			want:       true,
		},
		{
			name:       "both paths include sys mount prefix",
			path:       "/sys/devices/pci0000:00/usb3/3-4/3-4:1.0/sound/card3",
			usbSyspath: "/sys/devices/pci0000:00/usb3/3-4",
			want:       true,
		},
		{
			name:       "audio interface is a sibling of HID interface",
			path:       "/devices/pci0000:00/usb3/3-4/3-4:1.0/sound/card3",
			usbSyspath: "/sys/devices/pci0000:00/usb3/3-4/3-4:1.3",
			want:       false,
		},
		{
			name:       "neighboring USB device does not share component prefix",
			path:       "/devices/pci0000:00/usb3/3-40/3-40:1.0/sound/card4",
			usbSyspath: "/sys/devices/pci0000:00/usb3/3-4",
			want:       false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := belongsToUSB(test.path, test.usbSyspath); got != test.want {
				t.Fatalf("belongsToUSB(%q, %q) = %t, want %t",
					test.path, test.usbSyspath, got, test.want)
			}
		})
	}
}

func TestParseInspectPropertiesAcceptsUnescapedInnerQuotes(t *testing.T) {
	properties, err := parseInspectProperties([]byte(
		"iec958.codecs = \"[\"PCM\",\"DTS\",\"AC3\"]\"\n" +
			"node.name = \"headset\"\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	if got := properties["iec958.codecs"]; got != "[\"PCM\",\"DTS\",\"AC3\"]" {
		t.Fatalf("iec958.codecs = %q", got)
	}
}

func TestParseInspectPropertiesRejectsUnterminatedQuotedValue(t *testing.T) {
	_, err := parseInspectProperties([]byte("node.name = \"headset\n"))
	if err == nil || !strings.Contains(err.Error(), "invalid quoted value") {
		t.Fatalf("error = %v", err)
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

func TestWPCTLMapsEveryHeadsetVolumeLevel(t *testing.T) {
	responses := make([]commandResponse, 0, 32)
	for range 16 {
		responses = append(responses,
			commandResponse{result: CommandResult{Stdout: []byte("47\theadset\tAudio/Sink\n")}},
			commandResponse{},
		)
	}
	runner := &fakeCommandRunner{responses: responses}
	adapter := NewWPCTL(runner, time.Second)
	for level := uint8(0); level <= 15; level++ {
		if err := adapter.SetVolume(context.Background(), Target{Name: "headset"}, level); err != nil {
			t.Fatalf("level %d: %v", level, err)
		}
		args := runner.calls[int(level)*2+1].args
		if len(args) != 3 || args[0] != "set-volume" || args[1] != "47" {
			t.Fatalf("level %d args = %#v", level, args)
		}
		ratio, err := strconv.ParseFloat(args[2], 64)
		if err != nil || ratio != float64(level)/15 {
			t.Fatalf("level %d ratio = %q (%v)", level, args[2], err)
		}
	}
	if err := adapter.SetVolume(context.Background(), Target{Name: "headset"}, 16); err == nil {
		t.Fatal("out-of-range volume was accepted")
	}
}

func TestWPCTLSetMuteUsesCurrentSourceIDAndBooleanState(t *testing.T) {
	runner := &fakeCommandRunner{responses: []commandResponse{
		{result: CommandResult{Stdout: []byte("62\theadset-mic\tAudio/Source\n")}},
		{},
		{result: CommandResult{Stdout: []byte("91\theadset-mic\tAudio/Source\n")}},
		{},
	}}
	adapter := NewWPCTL(runner, time.Second)
	if err := adapter.SetMute(context.Background(), Target{Name: "headset-mic"}, true); err != nil {
		t.Fatal(err)
	}
	if err := adapter.SetMute(context.Background(), Target{Name: "headset-mic"}, false); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"set-mute", "62", "1"}, {"set-mute", "91", "0"}}
	got := [][]string{runner.calls[1].args, runner.calls[3].args}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mute calls = %#v, want %#v", got, want)
	}
}

func TestWPCTLControlsUseAutomaticResolver(t *testing.T) {
	responses := append(
		automaticResponses("20", "31", "headset")[:4],
		commandResponse{},
	)
	runner := &fakeCommandRunner{responses: responses}
	err := NewWPCTL(runner, time.Second).SetVolume(
		context.Background(),
		Target{USB: USBIdentity{Syspath: "/sys/usb/selected"}},
		8,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := runner.calls[len(runner.calls)-1].args; got[0] != "set-volume" || got[1] != "31" {
		t.Fatalf("automatic volume call = %#v", got)
	}
}

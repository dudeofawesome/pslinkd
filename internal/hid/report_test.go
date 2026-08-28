package hid

import (
	"errors"
	"reflect"
	"syscall"
	"testing"
)

func TestDecodeReportConnectionField(t *testing.T) {
	fixture := make([]byte, ReportLength)
	fixture[0] = ReportID

	report, err := DecodeReport(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if report.Connected {
		t.Fatal("clear connection bit decoded as connected")
	}

	fixture[39] = 0x01
	report, err = DecodeReport(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Connected {
		t.Fatal("set connection bit decoded as disconnected")
	}
}

func TestFixedDeviceProfile(t *testing.T) {
	if VendorID != 0x054c || ProductID != 0x0ecc || HIDInterface != 3 {
		t.Fatalf("unexpected device profile: %04x:%04x interface %d", VendorID, ProductID, HIDInterface)
	}
	if ProductID == 0x0fa3 {
		t.Fatal("unsupported PULSE 3D adapter must not match")
	}
}

func TestDecodeReportV11Fields(t *testing.T) {
	fixture := make([]byte, ReportLength)
	fixture[0] = ReportID
	fixture[39] = 0x39
	fixture[43] = 0xa0
	fixture[44] = 15
	report, err := DecodeReport(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Connected || !report.VolumeUpPressed || !report.VolumeDownPressed ||
		!report.MicrophoneMutePressed || report.MicrophoneMuted {
		t.Fatalf("decoded report = %#v", report)
	}
	if report.Volume == nil || *report.Volume != 15 || report.InvalidVolume != nil {
		t.Fatalf("decoded volume = %#v", report)
	}

	fixture[43] = 0x0f
	fixture[44] = 16
	report, err = DecodeReport(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if !report.MicrophoneMuted || report.Volume != nil ||
		report.InvalidVolume == nil || *report.InvalidVolume != 16 {
		t.Fatalf("invalid-volume report = %#v", report)
	}
}

func TestDecodeReportAcceptsEveryValidVolume(t *testing.T) {
	for value := uint8(0); value <= 15; value++ {
		fixture := make([]byte, ReportLength)
		fixture[0] = ReportID
		fixture[44] = value
		report, err := DecodeReport(fixture)
		if err != nil || report.Volume == nil || *report.Volume != value {
			t.Fatalf("volume %d decoded as %#v, error %v", value, report, err)
		}
	}
}

func TestDecodeReportRejectsWrongShape(t *testing.T) {
	if _, err := DecodeReport(make([]byte, ReportLength-1)); !errors.Is(err, ErrWrongLength) {
		t.Fatalf("wrong length error = %v", err)
	}
	fixture := make([]byte, ReportLength)
	if _, err := DecodeReport(fixture); !errors.Is(err, ErrWrongReportID) {
		t.Fatalf("wrong report ID error = %v", err)
	}
}

func TestFeatureRequest(t *testing.T) {
	if got := FeatureRequest(ReportLength); got != 0xC0404807 {
		t.Fatalf("HIDIOCGFEATURE(64) = %#x", got)
	}
}

func TestSetFeatureRequestAndTargetVolumePayload(t *testing.T) {
	if got := SetFeatureRequest(DeviceVolumeReportLength); got != 0xC0164806 {
		t.Fatalf("HIDIOCSFEATURE(22) = %#x", got)
	}
	want := make([]byte, DeviceVolumeReportLength)
	want[0], want[1], want[2] = 0xd0, 0x02, 0x16
	if got := TargetDeviceVolumePayload(); !reflect.DeepEqual(got, want) {
		t.Fatalf("target-volume payload = %x, want %x", got, want)
	}
}

func TestExpectedReadErrorClassification(t *testing.T) {
	for _, expected := range []error{
		syscall.EPIPE,
		syscall.ENODEV,
		syscall.EIO,
		syscall.ETIMEDOUT,
	} {
		if !IsExpectedReadError(expected) {
			t.Errorf("%v should be expected", expected)
		}
	}
	if IsExpectedReadError(syscall.EACCES) {
		t.Fatal("EACCES should not be an expected sample failure")
	}
}

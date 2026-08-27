package hid

import (
	"errors"
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

func TestDecodeReportIgnoresDeferredFields(t *testing.T) {
	fixture := make([]byte, ReportLength)
	fixture[0] = ReportID
	fixture[39] = 0x38
	fixture[43] = 0xf0
	fixture[44] = 15
	report, err := DecodeReport(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if report != (Report{}) {
		t.Fatalf("deferred fields changed v1 state: %#v", report)
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

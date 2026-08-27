package hid

import (
	"errors"
	"syscall"
)

const (
	VendorID     = uint16(0x054c)
	ProductID    = uint16(0x0ecc)
	HIDInterface = uint8(3)
	ReportID     = byte(0xB0)
	ReportLength = 64

	iocNRBits   = 8
	iocTypeBits = 8
	iocSizeBits = 14

	iocNRShift   = 0
	iocTypeShift = iocNRShift + iocNRBits
	iocSizeShift = iocTypeShift + iocTypeBits
	iocDirShift  = iocSizeShift + iocSizeBits

	iocWrite = 1
	iocRead  = 2
)

var (
	ErrWrongLength   = errors.New("HID feature report has wrong length")
	ErrWrongReportID = errors.New("HID feature report has wrong report ID")
)

type Report struct {
	Connected             bool
	VolumeUpPressed       bool
	VolumeDownPressed     bool
	MicrophoneMutePressed bool
	MicrophoneMuted       bool
	Volume                *uint8
	InvalidVolume         *uint8
}

func DecodeReport(data []byte) (Report, error) {
	if len(data) != ReportLength {
		return Report{}, ErrWrongLength
	}
	if data[0] != ReportID {
		return Report{}, ErrWrongReportID
	}
	volume := uint8(data[44])
	report := Report{
		Connected:             data[39]&0x01 != 0,
		VolumeUpPressed:       data[39]&0x08 != 0,
		VolumeDownPressed:     data[39]&0x10 != 0,
		MicrophoneMutePressed: data[39]&0x20 != 0,
		MicrophoneMuted:       data[43]&0xf0 == 0,
	}
	if volume <= 15 {
		report.Volume = &volume
	} else {
		report.InvalidVolume = &volume
	}
	return report, nil
}

func FeatureRequest(length uint32) uintptr {
	return uintptr((iocRead|iocWrite)<<iocDirShift |
		length<<iocSizeShift |
		uint32('H')<<iocTypeShift |
		0x07<<iocNRShift)
}

func IsExpectedReadError(err error) bool {
	return errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ENODEV) ||
		errors.Is(err, syscall.EIO) ||
		errors.Is(err, syscall.ETIMEDOUT)
}

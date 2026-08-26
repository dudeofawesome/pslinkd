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
	Connected bool
}

func DecodeReport(data []byte) (Report, error) {
	if len(data) != ReportLength {
		return Report{}, ErrWrongLength
	}
	if data[0] != ReportID {
		return Report{}, ErrWrongReportID
	}
	return Report{Connected: data[39]&0x01 != 0}, nil
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

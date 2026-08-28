//go:build linux

package hid

import (
	"fmt"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

type FeatureReader struct {
	mu   sync.Mutex
	file *os.File
}

func Open(path string) (*FeatureReader, error) {
	file, err := os.OpenFile(path, os.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open hidraw device %q: %w", path, err)
	}
	return &FeatureReader{file: file}, nil
}

func (reader *FeatureReader) ReadFeature() ([]byte, error) {
	reader.mu.Lock()
	if reader.file == nil {
		reader.mu.Unlock()
		return nil, os.ErrClosed
	}
	file := reader.file
	reader.mu.Unlock()

	buffer := make([]byte, ReportLength)
	buffer[0] = ReportID
	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		file.Fd(),
		FeatureRequest(ReportLength),
		uintptr(unsafe.Pointer(&buffer[0])),
	)
	if errno != 0 {
		return nil, fmt.Errorf("HIDIOCGFEATURE report 0x%02x: %w", ReportID, errno)
	}
	return buffer, nil
}

func (reader *FeatureReader) WriteFeature(payload []byte) error {
	if len(payload) != DeviceVolumeReportLength || payload[0] != DeviceVolumeReportID {
		return fmt.Errorf("invalid HID feature payload")
	}
	reader.mu.Lock()
	if reader.file == nil {
		reader.mu.Unlock()
		return os.ErrClosed
	}
	file := reader.file
	reader.mu.Unlock()

	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		file.Fd(),
		SetFeatureRequest(uint32(len(payload))),
		uintptr(unsafe.Pointer(&payload[0])),
	)
	if errno != 0 {
		return fmt.Errorf("HIDIOCSFEATURE report 0x%02x: %w", payload[0], errno)
	}
	return nil
}

func (reader *FeatureReader) Close() error {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.file == nil {
		return nil
	}
	err := reader.file.Close()
	reader.file = nil
	return err
}

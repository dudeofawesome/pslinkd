//go:build linux && cgo

package discovery

/*
#cgo pkg-config: libudev
#include <libudev.h>
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

type UdevBackend struct{}

func NewBackend() Backend {
	return &UdevBackend{}
}

func (*UdevBackend) Enumerate() ([]Candidate, error) {
	udev := C.udev_new()
	if udev == nil {
		return nil, errors.New("udev_new returned nil")
	}
	defer C.udev_unref(udev)

	enumerator := C.udev_enumerate_new(udev)
	if enumerator == nil {
		return nil, errors.New("udev_enumerate_new returned nil")
	}
	defer C.udev_enumerate_unref(enumerator)

	hidraw := C.CString("hidraw")
	defer C.free(unsafe.Pointer(hidraw))
	if result := C.udev_enumerate_add_match_subsystem(enumerator, hidraw); result < 0 {
		return nil, syscall.Errno(-result)
	}
	if result := C.udev_enumerate_scan_devices(enumerator); result < 0 {
		return nil, syscall.Errno(-result)
	}

	var candidates []Candidate
	for entry := C.udev_enumerate_get_list_entry(enumerator); entry != nil; entry = C.udev_list_entry_get_next(entry) {
		name := C.udev_list_entry_get_name(entry)
		if name == nil {
			continue
		}
		device := C.udev_device_new_from_syspath(udev, name)
		if device == nil {
			continue
		}
		candidate, matches := candidateFromDevice(device)
		C.udev_device_unref(device)
		if matches {
			candidates = append(candidates, candidate)
		}
	}
	return candidates, nil
}

func candidateFromDevice(device *C.struct_udev_device) (Candidate, bool) {
	usb := C.CString("usb")
	usbDeviceType := C.CString("usb_device")
	usbInterfaceType := C.CString("usb_interface")
	defer C.free(unsafe.Pointer(usb))
	defer C.free(unsafe.Pointer(usbDeviceType))
	defer C.free(unsafe.Pointer(usbInterfaceType))

	usbDevice := C.udev_device_get_parent_with_subsystem_devtype(device, usb, usbDeviceType)
	usbInterface := C.udev_device_get_parent_with_subsystem_devtype(device, usb, usbInterfaceType)
	if usbDevice == nil || usbInterface == nil {
		return Candidate{}, false
	}
	if sysattr(usbDevice, "idVendor") != "054c" ||
		sysattr(usbDevice, "idProduct") != "0ecc" ||
		sysattr(usbInterface, "bInterfaceNumber") != "03" {
		return Candidate{}, false
	}

	syspath := C.udev_device_get_syspath(device)
	devnode := C.udev_device_get_devnode(device)
	if syspath == nil || devnode == nil {
		return Candidate{}, false
	}
	return Candidate{Syspath: C.GoString(syspath), Devnode: C.GoString(devnode)}, true
}

func sysattr(device *C.struct_udev_device, name string) string {
	attribute := C.CString(name)
	defer C.free(unsafe.Pointer(attribute))
	value := C.udev_device_get_sysattr_value(device, attribute)
	if value == nil {
		return ""
	}
	return C.GoString(value)
}

func (*UdevBackend) Monitor(ctx context.Context, emit func(Event) error) error {
	udev := C.udev_new()
	if udev == nil {
		return errors.New("udev_new returned nil")
	}
	defer C.udev_unref(udev)

	channel := C.CString("udev")
	defer C.free(unsafe.Pointer(channel))
	monitor := C.udev_monitor_new_from_netlink(udev, channel)
	if monitor == nil {
		return errors.New("udev_monitor_new_from_netlink returned nil")
	}
	defer C.udev_monitor_unref(monitor)

	hidraw := C.CString("hidraw")
	defer C.free(unsafe.Pointer(hidraw))
	if result := C.udev_monitor_filter_add_match_subsystem_devtype(monitor, hidraw, nil); result < 0 {
		return syscall.Errno(-result)
	}
	if result := C.udev_monitor_enable_receiving(monitor); result < 0 {
		return syscall.Errno(-result)
	}

	fd := int(C.udev_monitor_get_fd(monitor))
	if fd < 0 {
		return errors.New("udev monitor returned invalid file descriptor")
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		pollDescriptors := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		ready, err := unix.Poll(pollDescriptors, 250)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return fmt.Errorf("poll udev monitor: %w", err)
		}
		if ready == 0 {
			continue
		}
		if pollDescriptors[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return fmt.Errorf("udev monitor poll failure: revents=%#x", pollDescriptors[0].Revents)
		}

		device := C.udev_monitor_receive_device(monitor)
		if device == nil {
			continue
		}
		action := C.udev_device_get_action(device)
		syspath := C.udev_device_get_syspath(device)
		event := Event{}
		if action != nil {
			event.Action = C.GoString(action)
		}
		if syspath != nil {
			event.Syspath = C.GoString(syspath)
		}
		C.udev_device_unref(device)
		if err := emit(event); err != nil {
			return err
		}
	}
}

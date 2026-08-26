//go:build !linux || !cgo

package discovery

import (
	"context"
	"errors"
	"runtime"
)

type unsupportedBackend struct{}

func NewBackend() Backend {
	return unsupportedBackend{}
}

func (unsupportedBackend) Enumerate() ([]Candidate, error) {
	return nil, errors.New("libudev discovery requires Linux with cgo; running on " + runtime.GOOS)
}

func (unsupportedBackend) Monitor(context.Context, func(Event) error) error {
	return errors.New("libudev discovery requires Linux with cgo")
}

//go:build !linux

package hid

import (
	"errors"
	"runtime"
)

type FeatureReader struct{}

func Open(string) (*FeatureReader, error) {
	return nil, errors.New("hidraw feature reports require Linux; running on " + runtime.GOOS)
}

func (*FeatureReader) ReadFeature() ([]byte, error) {
	return nil, errors.New("hidraw feature reports require Linux")
}

func (*FeatureReader) Close() error {
	return nil
}

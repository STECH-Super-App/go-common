package client

import "errors"

var (
	// ErrInvalidDeviceID is returned when the device ID is invalid.
	ErrInvalidDeviceID = errors.New("invalid device id")
	// ErrInvalidUserAgent is returned when the user agent is invalid.
	ErrInvalidUserAgent = errors.New("invalid user agent")
	// ErrInvalidIP is returned when the IP address is invalid.
	ErrInvalidIP = errors.New("invalid ip")
)

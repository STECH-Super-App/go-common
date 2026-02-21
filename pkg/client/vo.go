package client

import (
	"net"

	"github.com/mssola/user_agent"
)

// Client value object containing device data
type Client struct {
	deviceID  string
	userAgent string
	ip        string
}

// NewClient creates a new Client value object
func NewClient(deviceID, userAgent, ip string) (*Client, error) {
	if deviceID == "" {
		return nil, ErrInvalidDeviceID
	}

	ua := user_agent.New(userAgent)
	if userAgent == "" || ua.UA() == "" {
		return nil, ErrInvalidUserAgent
	}

	if net.ParseIP(ip) == nil {
		return nil, ErrInvalidIP
	}

	return &Client{
		deviceID:  deviceID,
		userAgent: userAgent,
		ip:        ip,
	}, nil
}

// GetDeviceID returns the deviceID
func (c *Client) GetDeviceID() string {
	return c.deviceID
}

// GetUserAgent returns the userAgent
func (c *Client) GetUserAgent() string {
	return c.userAgent
}

// GetIP returns the ip
func (c *Client) GetIP() string {
	return c.ip
}

// IsWeb returns true if the client is a web browser
func (c *Client) IsWeb() bool {
	ua := user_agent.New(c.userAgent)
	browser, _ := ua.Browser()
	return browser != ""
}

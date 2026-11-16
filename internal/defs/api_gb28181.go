package defs

import (
	"errors"
	"time"
)

// ErrGB28181DeviceNotFound is returned when a GB28181 device is not found.
var ErrGB28181DeviceNotFound = errors.New("device not found")

// ErrGB28181ChannelNotFound is returned when a GB28181 channel is not found.
var ErrGB28181ChannelNotFound = errors.New("channel not found")

// APIGB28181Channel is a channel of a GB28181 device.
type APIGB28181Channel struct {
	ID             string  `json:"id"`
	DeviceID       string  `json:"deviceId"`
	Name           string  `json:"name"`
	Manufacturer   string  `json:"manufacturer"`
	Model          string  `json:"model"`
	Address        string  `json:"address"`
	ParentID       string  `json:"parentId"`
	Online         bool    `json:"online"`
	PTZType        int     `json:"ptzType"`
	Longitude      float64 `json:"longitude"`
	Latitude       float64 `json:"latitude"`
	Path           string  `json:"path"`
	PathConfigured bool    `json:"pathConfigured"`
	PathConflict   bool    `json:"pathConflict"`
	PathConfigName string  `json:"pathConfigName,omitempty"`
}

// APIGB28181ChannelList is a list of GB28181 channels.
type APIGB28181ChannelList struct {
	ItemCount int                 `json:"itemCount"`
	PageCount int                 `json:"pageCount"`
	Items     []APIGB28181Channel `json:"items"`
}

// APIGB28181Device is a GB28181 device.
type APIGB28181Device struct {
	ID                string              `json:"id"`
	Address           string              `json:"address"`
	Transport         string              `json:"transport"`
	Online            bool                `json:"online"`
	RegisteredTime    *time.Time          `json:"registeredTime"`
	LastKeepaliveTime *time.Time          `json:"lastKeepaliveTime"`
	Name              string              `json:"name"`
	Manufacturer      string              `json:"manufacturer"`
	Model             string              `json:"model"`
	Firmware          string              `json:"firmware"`
	DeviceType        string              `json:"deviceType"`
	Channels          []APIGB28181Channel `json:"channels"`
}

// APIGB28181DeviceList is a list of GB28181 devices.
type APIGB28181DeviceList struct {
	ItemCount int                `json:"itemCount"`
	PageCount int                `json:"pageCount"`
	Items     []APIGB28181Device `json:"items"`
}

// APIGB28181RecordItem is a recording segment of a GB28181 channel.
type APIGB28181RecordItem struct {
	DeviceID   string `json:"deviceId"`
	Name       string `json:"name"`
	FilePath   string `json:"filePath"`
	Address    string `json:"address"`
	StartTime  string `json:"startTime"`
	EndTime    string `json:"endTime"`
	Secrecy    int    `json:"secrecy"`
	Type       string `json:"type"`
	RecorderID string `json:"recorderId,omitempty"`
	FileSize   string `json:"fileSize,omitempty"`
}

// APIGB28181RecordList is a list of recording segments of a GB28181 channel.
type APIGB28181RecordList struct {
	ItemCount int                    `json:"itemCount"`
	Items     []APIGB28181RecordItem `json:"items"`
}

// APIGB28181Server contains methods used by the API server.
type APIGB28181Server interface {
	APIDevicesList() (*APIGB28181DeviceList, error)
	APIDevicesGet(string) (*APIGB28181Device, error)
	APIChannelsList() (*APIGB28181ChannelList, error)
	APIRefreshCatalog(string) error
	APIPTZControl(string, string) error
	APIRecordInfo(channelID string, startTime time.Time, endTime time.Time, recordType string) (*APIGB28181RecordList, error)
}

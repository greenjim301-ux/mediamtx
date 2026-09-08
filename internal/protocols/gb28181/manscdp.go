// Package gb28181 contains GB/T 28181 protocol utilities.
package gb28181

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// command types of MANSCDP messages.
const (
	CmdTypeKeepalive     = "Keepalive"
	CmdTypeCatalog       = "Catalog"
	CmdTypeDeviceInfo    = "DeviceInfo"
	CmdTypeDeviceControl = "DeviceControl"
	CmdTypeDeviceStatus  = "DeviceStatus"
	CmdTypeAlarm         = "Alarm"
)

// CatalogItem is an item of a catalog response, that describes a channel.
type CatalogItem struct {
	DeviceID     string  `xml:"DeviceID"`
	Name         string  `xml:"Name"`
	Manufacturer string  `xml:"Manufacturer"`
	Model        string  `xml:"Model"`
	Owner        string  `xml:"Owner"`
	CivilCode    string  `xml:"CivilCode"`
	Address      string  `xml:"Address"`
	Parental     string  `xml:"Parental"`
	ParentID     string  `xml:"ParentID"`
	RegisterWay  int     `xml:"RegisterWay"`
	Secrecy      int     `xml:"Secrecy"`
	Status       string  `xml:"Status"`
	Longitude    float64 `xml:"Longitude"`
	Latitude     float64 `xml:"Latitude"`
	Info         struct {
		PTZType int `xml:"PTZType"`
	} `xml:"Info"`
}

// IsOnline returns whether the channel is online.
func (i CatalogItem) IsOnline() bool {
	s := strings.ToUpper(i.Status)
	return s == "ON" || s == "ONLINE" || s == "OK"
}

// Message is a MANSCDP message.
// It contains the union of the fields of all supported messages,
// since devices are not consistent in the way they fill them.
type Message struct {
	XMLName  xml.Name
	CmdType  string `xml:"CmdType"`
	SN       int    `xml:"SN"`
	DeviceID string `xml:"DeviceID"`

	// Keepalive, DeviceStatus
	Status string `xml:"Status"`

	// DeviceInfo
	DeviceType   string `xml:"DeviceType"`
	Manufacturer string `xml:"Manufacturer"`
	Model        string `xml:"Model"`
	Firmware     string `xml:"Firmware"`
	Channel      int    `xml:"Channel"`

	// Catalog
	SumNum     int `xml:"SumNum"`
	DeviceList struct {
		Num   int           `xml:"Num,attr"`
		Items []CatalogItem `xml:"Item"`
	} `xml:"DeviceList"`
}

// IsNotify returns whether the message is a notification.
func (m *Message) IsNotify() bool {
	return m.XMLName.Local == "Notify"
}

// IsResponse returns whether the message is a response.
func (m *Message) IsResponse() bool {
	return m.XMLName.Local == "Response"
}

func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(charset) {
	case "gb2312", "gbk", "gb18030":
		return transform.NewReader(input, simplifiedchinese.GBK.NewDecoder()), nil

	case "utf-8", "utf8", "":
		return input, nil
	}

	return nil, fmt.Errorf("unsupported charset: %s", charset)
}

// DecodeMessage decodes a MANSCDP message.
func DecodeMessage(buf []byte) (*Message, error) {
	// some devices declare a charset but send invalid characters,
	// and some send a body with trailing garbage: be tolerant.
	buf = bytes.TrimSpace(buf)
	if len(buf) == 0 {
		return nil, fmt.Errorf("empty body")
	}

	var msg Message

	dec := xml.NewDecoder(bytes.NewReader(buf))
	dec.CharsetReader = charsetReader
	dec.Strict = false

	err := dec.Decode(&msg)
	if err != nil {
		return nil, err
	}

	return &msg, nil
}

// EncodeQuery encodes a query message.
// Since the content is always ASCII, no charset conversion is needed.
func EncodeQuery(cmdType string, sn int, deviceID string) []byte {
	var buf strings.Builder
	buf.WriteString("<?xml version=\"1.0\" encoding=\"GB2312\"?>\r\n")
	buf.WriteString("<Query>\r\n")
	fmt.Fprintf(&buf, "<CmdType>%s</CmdType>\r\n", cmdType)
	fmt.Fprintf(&buf, "<SN>%d</SN>\r\n", sn)
	fmt.Fprintf(&buf, "<DeviceID>%s</DeviceID>\r\n", deviceID)
	buf.WriteString("</Query>\r\n")
	return []byte(buf.String())
}

// EncodeDeviceControlPTZ encodes a PTZ control message.
func EncodeDeviceControlPTZ(sn int, deviceID string, ptzCmd string) []byte {
	var buf strings.Builder
	buf.WriteString("<?xml version=\"1.0\" encoding=\"GB2312\"?>\r\n")
	buf.WriteString("<Control>\r\n")
	fmt.Fprintf(&buf, "<CmdType>%s</CmdType>\r\n", CmdTypeDeviceControl)
	fmt.Fprintf(&buf, "<SN>%d</SN>\r\n", sn)
	fmt.Fprintf(&buf, "<DeviceID>%s</DeviceID>\r\n", deviceID)
	fmt.Fprintf(&buf, "<PTZCmd>%s</PTZCmd>\r\n", ptzCmd)
	buf.WriteString("<Info>\r\n")
	buf.WriteString("<ControlPriority>5</ControlPriority>\r\n")
	buf.WriteString("</Info>\r\n")
	buf.WriteString("</Control>\r\n")
	return []byte(buf.String())
}

// EncodeResponse encodes a response to a notification.
func EncodeResponse(cmdType string, sn int, deviceID string) []byte {
	var buf strings.Builder
	buf.WriteString("<?xml version=\"1.0\" encoding=\"GB2312\"?>\r\n")
	buf.WriteString("<Response>\r\n")
	fmt.Fprintf(&buf, "<CmdType>%s</CmdType>\r\n", cmdType)
	fmt.Fprintf(&buf, "<SN>%d</SN>\r\n", sn)
	fmt.Fprintf(&buf, "<DeviceID>%s</DeviceID>\r\n", deviceID)
	buf.WriteString("<Result>OK</Result>\r\n")
	buf.WriteString("</Response>\r\n")
	return []byte(buf.String())
}

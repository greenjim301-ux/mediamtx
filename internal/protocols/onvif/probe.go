package onvif

import (
	"context"
	"encoding/xml"
	"fmt"
	"net"
	"strings"
	"time"
)

const probePayload = `<?xml version="1.0" encoding="utf-8"?>` +
	`<Envelope xmlns:tds="http://www.onvif.org/ver10/device/wsdl" ` +
	`xmlns="http://www.w3.org/2003/05/soap-envelope">` +
	`<Header>` +
	`<wsa:MessageID xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing">` +
	`uuid:a6f2b7ec-174c-4260-9348-54489e96a05f` +
	`</wsa:MessageID>` +
	`<wsa:To xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing">` +
	`urn:schemas-xmlsoap-org:ws:2005:04:discovery` +
	`</wsa:To>` +
	`<wsa:Action xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing">` +
	`http://schemas.xmlsoap.org/ws/2005:04:discovery/Probe` +
	`</wsa:Action>` +
	`</Header>` +
	`<Body>` +
	`<Probe xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" ` +
	`xmlns:xsd="http://www.w3.org/2001/XMLSchema" ` +
	`xmlns="http://schemas.xmlsoap.org/ws/2005/04/discovery">` +
	`<Types>tds:Device</Types>` +
	`<Scopes/>` +
	`</Probe>` +
	`</Body>` +
	`</Envelope>`

// Probe sends a unicast WS-Discovery Probe to the given host/IP on port 3702.
// It returns the first device service URL found in XAddrs.
func Probe(ctx context.Context, hostOrIP string, timeout time.Duration) (string, error) {
	host := hostOrIP
	port := "3702"
	if h, p, err := net.SplitHostPort(hostOrIP); err == nil {
		host = h
		if p != "" {
			port = p
		}
	}

	targetAddr := net.JoinHostPort(host, port)
	conn, err := net.DialTimeout("udp", targetAddr, timeout)
	if err != nil {
		return "", fmt.Errorf("failed to dial UDP %s: %w", targetAddr, err)
	}
	defer conn.Close()

	probeDeadline := time.Now().Add(timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(probeDeadline) {
		probeDeadline = dl
	}
	_ = conn.SetDeadline(probeDeadline)

	_, err = conn.Write([]byte(probePayload))
	if err != nil {
		return "", fmt.Errorf("failed to send WS-Discovery probe: %w", err)
	}

	buf := make([]byte, 8192)
	n, err := conn.Read(buf)
	if err != nil {
		return "", fmt.Errorf("probe read timeout or error: %w", err)
	}

	urls := ParseXAddrs(buf[:n])
	if len(urls) == 0 {
		return "", fmt.Errorf("no XAddrs found in probe response")
	}

	return urls[0], nil
}

// ParseXAddrs extracts all URLs found in any XAddrs elements from XML bytes.
func ParseXAddrs(data []byte) []string {
	var urls []string
	dec := xml.NewDecoder(strings.NewReader(string(data)))

	inXAddrs := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if strings.EqualFold(t.Name.Local, "XAddrs") {
				inXAddrs = true
			}
		case xml.EndElement:
			if strings.EqualFold(t.Name.Local, "XAddrs") {
				inXAddrs = false
			}
		case xml.CharData:
			if inXAddrs {
				fields := strings.Fields(string(t))
				for _, f := range fields {
					f = strings.TrimSpace(f)
					if f != "" {
						urls = append(urls, f)
					}
				}
			}
		}
	}

	return urls
}

// NormalizeDeviceURL ensures an ONVIF device service URL is formatted properly.
func NormalizeDeviceURL(rawIPOrURL string) string {
	raw := strings.TrimSpace(rawIPOrURL)
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}

	return "http://" + raw + "/onvif/device_service"
}

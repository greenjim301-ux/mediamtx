package onvif

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ProbeResult holds the information discovered from an ONVIF device.
type ProbeResult struct {
	DeviceServiceURL string `json:"deviceServiceUrl"`
	MediaURL         string `json:"mediaUrl"`
	PTZURL           string `json:"ptzUrl"`
	ProfileToken     string `json:"profileToken"`
	RTSPURL          string `json:"rtspUrl"`
	HasPTZ           bool   `json:"hasPtz"`
}

// Client provides ONVIF discovery and PTZ capabilities.
type Client struct {
	HTTPClient *http.Client
}

// NewClient creates a new ONVIF client.
func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *Client) soapPost(ctx context.Context, endpointURL, body string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewBufferString(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create SOAP request: %w", err)
	}

	req.Header.Set("Content-Type", "application/soap+xml; charset=utf-8")
	req.Header.Set("Accept", "*/*")

	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("SOAP request to %s failed: %w", endpointURL, err)
	}
	defer res.Body.Close()

	respBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		// Try to extract Fault text if available
		fault := parseSoapFault(respBytes)
		if fault != "" {
			return nil, fmt.Errorf("SOAP fault (status %d): %s", res.StatusCode, fault)
		}
		return nil, fmt.Errorf("SOAP request failed with HTTP status %d", res.StatusCode)
	}

	return respBytes, nil
}

// Discover executes the probe and inspection flow:
// 1. Unicast UDP WS-Discovery probe -> DeviceServiceURL (with HTTP fallback)
// 2. GetServices (or fallback GetCapabilities) -> MediaURL, PTZURL
// 3. GetProfiles -> ProfileToken
// 4. GetStreamUri -> RTSP URL with embedded credentials
func (c *Client) Discover(ctx context.Context, hostOrIP, user, pass string) (*ProbeResult, error) {
	var devServiceURL string
	probeTimeout := 1500 * time.Millisecond

	// 1. WS-Discovery UDP Probe
	probedURL, err := Probe(ctx, hostOrIP, probeTimeout)
	if err == nil && probedURL != "" {
		devServiceURL = probedURL
	} else {
		// Fallback to default device_service URL
		devServiceURL = NormalizeDeviceURL(hostOrIP)
	}

	// 2. GetServices
	mediaURL, ptzURL, err := c.getServices(ctx, devServiceURL, user, pass)
	if err != nil || mediaURL == "" {
		// Fallback to GetCapabilities
		var capErr error
		mediaURL, ptzURL, capErr = c.getCapabilities(ctx, devServiceURL, user, pass)
		if capErr != nil && mediaURL == "" {
			if err != nil {
				return nil, fmt.Errorf("failed to get device services: %w", err)
			}
			return nil, fmt.Errorf("failed to get device capabilities: %w", capErr)
		}
	}

	if mediaURL == "" {
		return nil, errors.New("device did not provide a media service URL")
	}

	// Fix device URL host if returned URL uses an internal or mismatched host
	mediaURL = reconcileHost(mediaURL, hostOrIP)
	if ptzURL != "" {
		ptzURL = reconcileHost(ptzURL, hostOrIP)
	}

	// 3. GetProfiles
	profileToken, err := c.getProfiles(ctx, mediaURL, user, pass)
	if err != nil {
		return nil, fmt.Errorf("failed to get media profiles: %w", err)
	}
	if profileToken == "" {
		return nil, errors.New("no profile token returned by media service")
	}

	// 4. GetStreamUri
	rawRTSP, err := c.getStreamURI(ctx, mediaURL, profileToken, user, pass)
	if err != nil {
		return nil, fmt.Errorf("failed to get RTSP stream URI: %w", err)
	}
	if rawRTSP == "" {
		return nil, errors.New("empty RTSP URI returned by media service")
	}

	// Reconcile stream host if device reports internal/private IP
	finalRTSP := reconcileRTSPHost(rawRTSP, hostOrIP)

	// Embed user:pass credentials into RTSP URL if needed
	if user != "" || pass != "" {
		finalRTSP = embedCredentials(finalRTSP, user, pass)
	}

	res := &ProbeResult{
		DeviceServiceURL: devServiceURL,
		MediaURL:         mediaURL,
		PTZURL:           ptzURL,
		ProfileToken:     profileToken,
		RTSPURL:          finalRTSP,
		HasPTZ:           ptzURL != "" && profileToken != "",
	}

	return res, nil
}

func (c *Client) getServices(ctx context.Context, devURL, user, pass string) (string, string, error) {
	body := `<GetServices xmlns="http://www.onvif.org/ver10/device/wsdl">` +
		`<IncludeCapability>false</IncludeCapability>` +
		`</GetServices>`
	soap := WrapSOAP(user, pass, body)

	resp, err := c.soapPost(ctx, devURL, soap)
	if err != nil {
		return "", "", err
	}

	var mediaURL, ptzURL string
	dec := xml.NewDecoder(bytes.NewReader(resp))

	var currentNS, currentXAddr string
	inService := false

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			local := t.Name.Local
			if strings.EqualFold(local, "Service") {
				inService = true
				currentNS = ""
				currentXAddr = ""
			} else if inService && strings.EqualFold(local, "Namespace") {
				var ns string
				_ = dec.DecodeElement(&ns, &t)
				currentNS = strings.TrimSpace(ns)
			} else if inService && strings.EqualFold(local, "XAddr") {
				var xaddr string
				_ = dec.DecodeElement(&xaddr, &t)
				currentXAddr = strings.TrimSpace(xaddr)
			}
		case xml.EndElement:
			if strings.EqualFold(t.Name.Local, "Service") {
				inService = false
				if strings.Contains(currentNS, "/media/wsdl") || strings.Contains(currentNS, "ver10/media") {
					if mediaURL == "" {
						mediaURL = currentXAddr
					}
				}
				if strings.Contains(currentNS, "/ptz/wsdl") {
					if ptzURL == "" {
						ptzURL = currentXAddr
					}
				}
			}
		}
	}

	return mediaURL, ptzURL, nil
}

func (c *Client) getCapabilities(ctx context.Context, devURL, user, pass string) (string, string, error) {
	body := `<GetCapabilities xmlns="http://www.onvif.org/ver10/device/wsdl">` +
		`<Category>All</Category>` +
		`</GetCapabilities>`
	soap := WrapSOAP(user, pass, body)

	resp, err := c.soapPost(ctx, devURL, soap)
	if err != nil {
		return "", "", err
	}

	var mediaURL, ptzURL string
	dec := xml.NewDecoder(bytes.NewReader(resp))

	inMedia := false
	inPTZ := false

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			local := t.Name.Local
			switch {
			case strings.EqualFold(local, "Media"):
				inMedia = true
			case strings.EqualFold(local, "PTZ"):
				inPTZ = true
			case strings.EqualFold(local, "XAddr"):
				var xaddr string
				_ = dec.DecodeElement(&xaddr, &t)
				xaddr = strings.TrimSpace(xaddr)
				if inMedia && mediaURL == "" {
					mediaURL = xaddr
				}
				if inPTZ && ptzURL == "" {
					ptzURL = xaddr
				}
			}
		case xml.EndElement:
			local := t.Name.Local
			if strings.EqualFold(local, "Media") {
				inMedia = false
			} else if strings.EqualFold(local, "PTZ") {
				inPTZ = false
			}
		}
	}

	return mediaURL, ptzURL, nil
}

func (c *Client) getProfiles(ctx context.Context, mediaURL, user, pass string) (string, error) {
	body := `<GetProfiles xmlns="http://www.onvif.org/ver10/media/wsdl"/>`
	soap := WrapSOAP(user, pass, body)

	resp, err := c.soapPost(ctx, mediaURL, soap)
	if err != nil {
		return "", err
	}

	dec := xml.NewDecoder(bytes.NewReader(resp))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}

		if t, ok := tok.(xml.StartElement); ok {
			if strings.EqualFold(t.Name.Local, "Profiles") {
				for _, attr := range t.Attr {
					if strings.EqualFold(attr.Name.Local, "token") && attr.Value != "" {
						return attr.Value, nil
					}
				}
			}
		}
	}

	return "", errors.New("no Profiles token found in response")
}

func (c *Client) getStreamURI(ctx context.Context, mediaURL, profileToken, user, pass string) (string, error) {
	body := fmt.Sprintf(`<GetStreamUri xmlns="http://www.onvif.org/ver10/media/wsdl">`+
		`<StreamSetup>`+
		`<Stream xmlns="http://www.onvif.org/ver10/schema">RTP-Unicast</Stream>`+
		`<Transport xmlns="http://www.onvif.org/ver10/schema">`+
		`<Protocol>UDP</Protocol>`+
		`</Transport>`+
		`</StreamSetup>`+
		`<ProfileToken>%s</ProfileToken>`+
		`</GetStreamUri>`, profileToken)
	soap := WrapSOAP(user, pass, body)

	resp, err := c.soapPost(ctx, mediaURL, soap)
	if err != nil {
		return "", err
	}

	dec := xml.NewDecoder(bytes.NewReader(resp))
	inMediaURI := false

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if strings.EqualFold(t.Name.Local, "MediaUri") {
				inMediaURI = true
			} else if (inMediaURI || strings.EqualFold(t.Name.Local, "GetStreamUriResponse")) && strings.EqualFold(t.Name.Local, "Uri") {
				var uri string
				_ = dec.DecodeElement(&uri, &t)
				return strings.TrimSpace(uri), nil
			}
		case xml.EndElement:
			if strings.EqualFold(t.Name.Local, "MediaUri") {
				inMediaURI = false
			}
		}
	}

	return "", errors.New("no stream Uri element found in response")
}

// ContinuousMove sends an ONVIF ContinuousMove command to the PTZ service.
// panX, tiltY, zoom are in [-1.0, 1.0].
func (c *Client) ContinuousMove(ctx context.Context, ptzURL, profileToken, user, pass string, panX, tiltY, zoom float64) error {
	if ptzURL == "" {
		return errors.New("PTZ URL is empty")
	}
	if profileToken == "" {
		return errors.New("Profile token is empty")
	}

	body := fmt.Sprintf(`<ContinuousMove xmlns="http://www.onvif.org/ver20/ptz/wsdl">`+
		`<ProfileToken>%s</ProfileToken>`+
		`<Velocity>`+
		`<PanTilt xmlns="http://www.onvif.org/ver10/schema" x="%.4f" y="%.4f"/>`+
		`<Zoom xmlns="http://www.onvif.org/ver10/schema" x="%.4f"/>`+
		`</Velocity>`+
		`</ContinuousMove>`,
		profileToken, panX, tiltY, zoom)
	soap := WrapSOAP(user, pass, body)

	_, err := c.soapPost(ctx, ptzURL, soap)
	return err
}

// Stop sends an ONVIF Stop command to the PTZ service.
func (c *Client) Stop(ctx context.Context, ptzURL, profileToken, user, pass string, stopPanTilt, stopZoom bool) error {
	if ptzURL == "" {
		return errors.New("PTZ URL is empty")
	}
	if profileToken == "" {
		return errors.New("Profile token is empty")
	}

	body := fmt.Sprintf(`<Stop xmlns="http://www.onvif.org/ver20/ptz/wsdl">`+
		`<ProfileToken>%s</ProfileToken>`+
		`<PanTilt>%t</PanTilt>`+
		`<Zoom>%t</Zoom>`+
		`</Stop>`,
		profileToken, stopPanTilt, stopZoom)
	soap := WrapSOAP(user, pass, body)

	_, err := c.soapPost(ctx, ptzURL, soap)
	return err
}

func parseSoapFault(xmlBytes []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(xmlBytes))
	inFault := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if strings.EqualFold(t.Name.Local, "Fault") {
				inFault = true
			} else if inFault && (strings.EqualFold(t.Name.Local, "Text") || strings.EqualFold(t.Name.Local, "faultstring")) {
				var text string
				_ = dec.DecodeElement(&text, &t)
				return text
			}
		case xml.EndElement:
			if strings.EqualFold(t.Name.Local, "Fault") {
				inFault = false
			}
		}
	}
	return ""
}

func reconcileHost(serviceURL, targetHostOrIP string) string {
	u, err := url.Parse(serviceURL)
	if err != nil {
		return serviceURL
	}

	targetHost := targetHostOrIP
	if h, _, err := net.SplitHostPort(targetHostOrIP); err == nil {
		targetHost = h
	}

	// If serviceURL uses localhost or 0.0.0.0 or a non-routable host, replace host with targetHost
	hostOnly := u.Hostname()
	if hostOnly == "localhost" || hostOnly == "127.0.0.1" || hostOnly == "0.0.0.0" {
		port := u.Port()
		if port != "" {
			u.Host = net.JoinHostPort(targetHost, port)
		} else {
			u.Host = targetHost
		}
		return u.String()
	}

	return serviceURL
}

func reconcileRTSPHost(rawRTSP, targetHostOrIP string) string {
	u, err := url.Parse(rawRTSP)
	if err != nil {
		return rawRTSP
	}

	targetHost := targetHostOrIP
	if h, _, err := net.SplitHostPort(targetHostOrIP); err == nil {
		targetHost = h
	}

	hostOnly := u.Hostname()
	if hostOnly == "localhost" || hostOnly == "127.0.0.1" || hostOnly == "0.0.0.0" {
		port := u.Port()
		if port != "" {
			u.Host = net.JoinHostPort(targetHost, port)
		} else {
			u.Host = targetHost
		}
		return u.String()
	}

	return rawRTSP
}

func embedCredentials(rawRTSP, user, pass string) string {
	if !strings.HasPrefix(rawRTSP, "rtsp://") && !strings.HasPrefix(rawRTSP, "rtsps://") {
		return rawRTSP
	}

	schemeIdx := strings.Index(rawRTSP, "://")
	if schemeIdx == -1 {
		return rawRTSP
	}

	prefix := rawRTSP[:schemeIdx+3]
	rest := rawRTSP[schemeIdx+3:]

	// If rest already has '@', credentials are already embedded
	firstSlash := strings.Index(rest, "/")
	authority := rest
	if firstSlash != -1 {
		authority = rest[:firstSlash]
	}
	if strings.Contains(authority, "@") {
		return rawRTSP
	}

	return prefix + url.QueryEscape(user) + ":" + url.QueryEscape(pass) + "@" + rest
}

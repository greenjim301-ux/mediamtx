package onvif

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"time"
)

// GenerateWSSecurity generates WS-Security UsernameToken header for ONVIF SOAP requests.
// PasswordDigest = Base64( SHA1( RawNonce + Created + Password ) )
func GenerateWSSecurity(user, password string) string {
	rawNonce := make([]byte, 20)
	_, _ = rand.Read(rawNonce)

	nonce := base64.StdEncoding.EncodeToString(rawNonce)
	created := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	h := sha1.New()
	h.Write(rawNonce)
	h.Write([]byte(created))
	h.Write([]byte(password))
	digest := base64.StdEncoding.EncodeToString(h.Sum(nil))

	return fmt.Sprintf(`<s:Header>`+
		`<wsse:Security xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd" `+
		`xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">`+
		`<wsse:UsernameToken>`+
		`<wsse:Username>%s</wsse:Username>`+
		`<wsse:Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">%s</wsse:Password>`+
		`<wsse:Nonce>%s</wsse:Nonce>`+
		`<wsu:Created>%s</wsu:Created>`+
		`</wsse:UsernameToken>`+
		`</wsse:Security>`+
		`</s:Header>`,
		user, digest, nonce, created)
}

// WrapSOAP wraps a SOAP body with an optional WS-Security header inside an Envelope.
func WrapSOAP(user, password, body string) string {
	var header string
	if user != "" || password != "" {
		header = GenerateWSSecurity(user, password)
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>`+
		`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">`+
		`%s`+
		`<s:Body xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">`+
		`%s`+
		`</s:Body>`+
		`</s:Envelope>`,
		header, body)
}

package handlers

import (
	"bytes"
	"encoding/xml"
	"strconv"

	"github.com/Shagrat2/go-autoconfig/config"
)

const (
	autodiscoverNS = "http://schemas.microsoft.com/exchange/autodiscover/responseschema/2006"

	responseSchema2006  = "http://schemas.microsoft.com/exchange/autodiscover/responseschema/2006"
	responseSchema2006a = "http://schemas.microsoft.com/exchange/autodiscover/outlook/responseschema/2006a"

	plistDoctype = `<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n"
)

type autodiscoverDoc struct {
	XMLName  xml.Name         `xml:"Autodiscover"`
	Xmlns    string           `xml:"xmlns,attr"`
	Response autodiscoverBody `xml:"Response"`
}

type autodiscoverBody struct {
	Xmlns    string             `xml:"xmlns,attr"`
	User     autodiscoverUser   `xml:"User"`
	Accounts []autodiscoverAcct `xml:"Account"`
}

type autodiscoverUser struct {
	DisplayName             string `xml:"DisplayName"`
	AutoDiscoverSMTPAddress string `xml:"AutoDiscoverSMTPAddress"`
}

type autodiscoverAcct struct {
	AccountType string               `xml:"AccountType"`
	Action      string               `xml:"Action"`
	Protocol    autodiscoverProtocol `xml:"Protocol"`
}

type autodiscoverProtocol struct {
	Type           string `xml:"Type"`
	TTL            int    `xml:"TTL"`
	Server         string `xml:"Server"`
	Port           int    `xml:"Port"`
	LoginName      string `xml:"LoginName"`
	DomainRequired string `xml:"DomainRequired"`
	DomainName     string `xml:"DomainName"`
	SPA            string `xml:"SPA"`
	TLS            string `xml:"TLS,omitempty"`
	SSL            string `xml:"SSL,omitempty"`
	AuthRequired   string `xml:"AuthRequired"`
}

func buildAutodiscover(d config.Domain, domain, email, schema string) autodiscoverDoc {
	return autodiscoverDoc{
		Xmlns: autodiscoverNS,
		Response: autodiscoverBody{
			Xmlns: schema,
			User: autodiscoverUser{
				DisplayName:             email,
				AutoDiscoverSMTPAddress: email,
			},
			Accounts: []autodiscoverAcct{
				buildAccount("IMAP", d.IMAP, domain, email),
				buildAccount("SMTP", d.SMTP, domain, email),
			},
		},
	}
}

func buildAccount(kind string, s config.Server, domain, email string) autodiscoverAcct {
	p := autodiscoverProtocol{
		Type:           kind,
		TTL:            1,
		Server:         s.Host,
		Port:           s.Port,
		LoginName:      email,
		DomainRequired: "on",
		DomainName:     domain,
		SPA:            "off",
		AuthRequired:   "on",
	}
	if s.STARTTLS {
		p.TLS = "on"
	} else {
		p.SSL = "on"
	}

	return autodiscoverAcct{
		AccountType: "email",
		Action:      "settings",
		Protocol:    p,
	}
}

func responseSchema(s string) (string, bool) {
	switch s {
	case "":
		return responseSchema2006a, true
	case responseSchema2006, responseSchema2006a:
		return s, true
	default:
		return "", false
	}
}

type clientConfigDoc struct {
	XMLName  xml.Name      `xml:"clientConfig"`
	Version  string        `xml:"version,attr"`
	Provider emailProvider `xml:"emailProvider"`
}

type emailProvider struct {
	ID               string     `xml:"id,attr"`
	Domain           string     `xml:"domain"`
	DisplayName      string     `xml:"displayName"`
	DisplayShortName string     `xml:"displayShortName"`
	Incoming         mailServer `xml:"incomingServer"`
	Outgoing         mailServer `xml:"outgoingServer"`
}

type mailServer struct {
	Type           string `xml:"type,attr"`
	Hostname       string `xml:"hostname"`
	Port           int    `xml:"port"`
	SocketType     string `xml:"socketType"`
	Authentication string `xml:"authentication"`
	Username       string `xml:"username"`
}

func buildClientConfig(d config.Domain, domain string) clientConfigDoc {
	return clientConfigDoc{
		Version: "1.1",
		Provider: emailProvider{
			ID:               domain,
			Domain:           domain,
			DisplayName:      "%EMAILADDRESS%",
			DisplayShortName: "%EMAILLOCALPART%",
			Incoming:         buildMailServer("imap", d.IMAP),
			Outgoing:         buildMailServer("smtp", d.SMTP),
		},
	}
}

func buildMailServer(kind string, s config.Server) mailServer {
	socketType := "SSL"
	if s.STARTTLS {
		socketType = "STARTTLS"
	}

	return mailServer{
		Type:           kind,
		Hostname:       s.Host,
		Port:           s.Port,
		SocketType:     socketType,
		Authentication: "password-cleartext",
		Username:       "%EMAILADDRESS%",
	}
}

type plistDocument struct {
	dict plistDict
}

func (d plistDocument) MarshalXML(e *xml.Encoder, _ xml.StartElement) error {
	start := xml.StartElement{
		Name: xml.Name{Local: "plist"},
		Attr: []xml.Attr{{Name: xml.Name{Local: "version"}, Value: "1.0"}},
	}
	if err := e.EncodeToken(start); err != nil {
		return err
	}
	if err := d.dict.writeValue(e); err != nil {
		return err
	}

	return e.EncodeToken(xml.EndElement{Name: start.Name})
}

type plistEntry struct {
	key string
	val plistValue
}

type plistValue interface {
	writeValue(*xml.Encoder) error
}

type plistDict []plistEntry

func (d plistDict) writeValue(e *xml.Encoder) error {
	start := xml.StartElement{Name: xml.Name{Local: "dict"}}
	if err := e.EncodeToken(start); err != nil {
		return err
	}
	for _, entry := range d {
		if err := writeText(e, "key", entry.key); err != nil {
			return err
		}
		if err := entry.val.writeValue(e); err != nil {
			return err
		}
	}

	return e.EncodeToken(xml.EndElement{Name: start.Name})
}

type plistArray []plistValue

func (a plistArray) writeValue(e *xml.Encoder) error {
	start := xml.StartElement{Name: xml.Name{Local: "array"}}
	if err := e.EncodeToken(start); err != nil {
		return err
	}
	for _, v := range a {
		if err := v.writeValue(e); err != nil {
			return err
		}
	}

	return e.EncodeToken(xml.EndElement{Name: start.Name})
}

type plistString string

func (v plistString) writeValue(e *xml.Encoder) error {
	return writeText(e, "string", string(v))
}

type plistInteger int

func (v plistInteger) writeValue(e *xml.Encoder) error {
	return writeText(e, "integer", strconv.Itoa(int(v)))
}

type plistReal string

func (v plistReal) writeValue(e *xml.Encoder) error {
	return writeText(e, "real", string(v))
}

type plistBool bool

func (v plistBool) writeValue(e *xml.Encoder) error {
	name := "false"
	if bool(v) {
		name = "true"
	}

	return writeEmpty(e, name)
}

func writeText(e *xml.Encoder, name, value string) error {
	start := xml.StartElement{Name: xml.Name{Local: name}}
	if err := e.EncodeToken(start); err != nil {
		return err
	}
	if err := e.EncodeToken(xml.CharData(value)); err != nil {
		return err
	}

	return e.EncodeToken(xml.EndElement{Name: start.Name})
}

func writeEmpty(e *xml.Encoder, name string) error {
	n := xml.Name{Local: name}
	if err := e.EncodeToken(xml.StartElement{Name: n}); err != nil {
		return err
	}

	return e.EncodeToken(xml.EndElement{Name: n})
}

func buildAppleProfile(d config.Domain, domain string) plistDocument {
	email := "user@" + domain

	account := plistDict{
		{key: "EmailAccountDescription", val: plistString(email)},
		{key: "EmailAccountName", val: plistString(email)},
		{key: "EmailAccountType", val: plistString("EmailTypeIMAP")},
		{key: "EmailAddress", val: plistString(email)},
		{key: "IncomingMailServerAuthentication", val: plistString("EmailAuthPassword")},
		{key: "IncomingMailServerHostName", val: plistString(d.IMAP.Host)},
		{key: "IncomingMailServerPortNumber", val: plistInteger(d.IMAP.Port)},
		{key: "IncomingMailServerUseSSL", val: plistBool(!d.IMAP.STARTTLS)},
		{key: "IncomingMailServerUsername", val: plistString(email)},
		{key: "OutgoingMailServerAuthentication", val: plistString("EmailAuthPassword")},
		{key: "OutgoingMailServerHostName", val: plistString(d.SMTP.Host)},
		{key: "OutgoingMailServerPortNumber", val: plistInteger(d.SMTP.Port)},
		{key: "OutgoingMailServerUseSSL", val: plistBool(!d.SMTP.STARTTLS)},
		{key: "OutgoingMailServerUsername", val: plistString(email)},
		{key: "OutgoingPasswordSameAsIncomingPassword", val: plistBool(true)},
		{key: "PayloadDescription", val: plistString("Configure Email Settings")},
		{key: "PayloadDisplayName", val: plistString(email)},
		{key: "PayloadIdentifier", val: plistString("com.l11r.goautoconfig.com.apple.mail.managed.7A981A9E-D5D0-4EF8-87FE-39FD6A506FAC")},
		{key: "PayloadType", val: plistString("com.apple.mail.managed")},
		{key: "PayloadUUID", val: plistString("7A981A9E-D5D0-4EF8-87FE-39FD6A506FAC")},
		{key: "PayloadVersion", val: plistReal("1")},
		{key: "SMIMEEnablePerMessageSwitch", val: plistBool(false)},
		{key: "SMIMEEnabled", val: plistBool(false)},
		{key: "disableMailRecentsSyncing", val: plistBool(false)},
	}

	return plistDocument{dict: plistDict{
		{key: "HasRemovalPasscode", val: plistBool(false)},
		{key: "PayloadContent", val: plistArray{account}},
		{key: "PayloadDescription", val: plistString("Configure Email Settings")},
		{key: "PayloadDisplayName", val: plistString(email)},
		{key: "PayloadIdentifier", val: plistString("com.l11r.goautoconfig")},
		{key: "PayloadOrganization", val: plistString(domain)},
		{key: "PayloadRemovalDisallowed", val: plistBool(false)},
		{key: "PayloadType", val: plistString("Configuration")},
		{key: "PayloadUUID", val: plistString("48C88203-4DB9-49E8-B593-4831903605A0")},
		{key: "PayloadVersion", val: plistInteger(1)},
	}}
}

func marshalXML(v any) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(xml.Header)

	enc := xml.NewEncoder(&buf)
	enc.Indent("", "\t")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Flush(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func marshalPlist(doc plistDocument) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	buf.WriteString(plistDoctype)

	enc := xml.NewEncoder(&buf)
	enc.Indent("", "\t")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Flush(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

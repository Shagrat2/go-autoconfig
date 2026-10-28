package handlers

import (
	"bytes"
	"encoding/xml"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Shagrat2/go-autoconfig/config"
)

var updateGolden = flag.Bool("update", false, "update golden files")

func testConfig() *config.Config {
	return &config.Config{
		Listen: ":0",
		Domains: map[string]config.Domain{
			"example.com": {
				IMAP: config.Server{Host: "imap.example.com", Port: 993},
				SMTP: config.Server{Host: "smtp.example.com", Port: 465},
			},
		},
	}
}

const outlookRequest = `<?xml version="1.0"?><Autodiscover><Request><EMailAddress>user@example.com</EMailAddress><AcceptableResponseSchema>` + responseSchema2006a + `</AcceptableResponseSchema></Request></Autodiscover>`

func doRequest(t *testing.T, cfg *config.Config, method, target, host, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	r := httptest.NewRequest(method, target, reader)
	r.Host = host
	if body != "" {
		r.Header.Set("Content-Type", "text/xml")
	}

	rec := httptest.NewRecorder()
	NewRouter(cfg).ServeHTTP(rec, r)

	return rec
}

func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()

	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatalf("write golden: %v", err)
		}

		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run go test ./handlers -update): %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("golden mismatch for %s\nwant:\n%s\ngot:\n%s", name, want, got)
	}
}

func assertWellFormedXML(t *testing.T, body string) {
	t.Helper()

	dec := xml.NewDecoder(strings.NewReader(body))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("response is not well-formed XML: %v\n%s", err, body)
		}
	}
}

func TestGoldenOutlook(t *testing.T) {
	rec := doRequest(t, testConfig(), http.MethodPost, "/autodiscover/autodiscover.xml", "autodiscover.example.com", outlookRequest)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	compareGolden(t, "outlook.xml", rec.Body.Bytes())
}

func TestGoldenThunderbird(t *testing.T) {
	rec := doRequest(t, testConfig(), http.MethodGet, "/mail/config-v1.1.xml", "autoconfig.example.com", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	compareGolden(t, "thunderbird.xml", rec.Body.Bytes())
}

func TestGoldenApple(t *testing.T) {
	cfg := testConfig()
	cfg.EnableApple = true

	rec := doRequest(t, cfg, http.MethodGet, "/email.mobileconfig", "autoconfig.example.com", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	compareGolden(t, "apple.mobileconfig", rec.Body.Bytes())
}

func TestOutlookEscapesEmail(t *testing.T) {
	const payload = `x</LoginName><LoginName>evil`

	rec := doRequest(t, testConfig(), http.MethodPost, "/autodiscover/autodiscover.xml", "autodiscover.example.com",
		`<?xml version="1.0"?><Autodiscover><Request><EMailAddress>x&lt;/LoginName&gt;&lt;LoginName&gt;evil</EMailAddress><AcceptableResponseSchema>`+responseSchema2006a+`</AcceptableResponseSchema></Request></Autodiscover>`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	assertWellFormedXML(t, body)

	if strings.Contains(body, "<LoginName>evil</LoginName>") {
		t.Fatalf("response contains injected element:\n%s", body)
	}
	if !strings.Contains(body, "x&lt;/LoginName&gt;&lt;LoginName&gt;evil") {
		t.Fatalf("response does not contain escaped payload:\n%s", body)
	}

	var got struct {
		User struct {
			DisplayName string `xml:"DisplayName"`
		} `xml:"Response>User"`
		Accounts []struct {
			Protocol struct {
				LoginName string `xml:"LoginName"`
			} `xml:"Protocol"`
		} `xml:"Response>Account"`
	}
	if err := xml.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.User.DisplayName != payload {
		t.Errorf("DisplayName = %q, want %q", got.User.DisplayName, payload)
	}
	if len(got.Accounts) != 2 {
		t.Fatalf("accounts = %d, want 2", len(got.Accounts))
	}
	for i, acc := range got.Accounts {
		if acc.Protocol.LoginName != payload {
			t.Errorf("account %d LoginName = %q, want %q", i, acc.Protocol.LoginName, payload)
		}
	}
}

func TestOutlookEscapesSchema(t *testing.T) {
	rec := doRequest(t, testConfig(), http.MethodPost, "/autodiscover/autodiscover.xml", "autodiscover.example.com",
		`<?xml version="1.0"?><Autodiscover><Request><EMailAddress>user@example.com</EMailAddress><AcceptableResponseSchema>http://evil/2006a"/&gt;&lt;x a="</AcceptableResponseSchema></Request></Autodiscover>`)

	body := rec.Body.String()
	assertWellFormedXML(t, body)

	if strings.Contains(body, "<x a=") {
		t.Fatalf("response contains injected element:\n%s", body)
	}

	var got struct {
		Response struct {
			Xmlns string `xml:"xmlns,attr"`
		} `xml:"Response"`
	}
	if err := xml.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Response.Xmlns != responseSchema2006a {
		t.Errorf("Response xmlns = %q, want %q", got.Response.Xmlns, responseSchema2006a)
	}
}

func TestOutlookKeepsKnownSchema(t *testing.T) {
	rec := doRequest(t, testConfig(), http.MethodPost, "/autodiscover/autodiscover.xml", "autodiscover.example.com",
		`<?xml version="1.0"?><Autodiscover><Request><EMailAddress>user@example.com</EMailAddress><AcceptableResponseSchema>`+responseSchema2006+`</AcceptableResponseSchema></Request></Autodiscover>`)

	if !strings.Contains(rec.Body.String(), `xmlns="`+responseSchema2006+`"`) {
		t.Fatalf("requested schema was not preserved:\n%s", rec.Body.String())
	}
}

func TestOutlookRejectsMalformedXML(t *testing.T) {
	rec := doRequest(t, testConfig(), http.MethodPost, "/autodiscover/autodiscover.xml", "autodiscover.example.com", `<Autodiscover><Request>`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestOutlookRejectsOversizedBody(t *testing.T) {
	rec := doRequest(t, testConfig(), http.MethodPost, "/autodiscover/autodiscover.xml", "autodiscover.example.com", strings.Repeat("a", maxBodyBytes+1))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestOutlookGetWithoutBody(t *testing.T) {
	rec := doRequest(t, testConfig(), http.MethodGet, "/autodiscover/autodiscover.xml", "autodiscover.example.com", "")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCaseInsensitivePath(t *testing.T) {
	rec := doRequest(t, testConfig(), http.MethodPost, "/Autodiscover/Autodiscover.xml", "autodiscover.example.com", outlookRequest)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHostWithPort(t *testing.T) {
	rec := doRequest(t, testConfig(), http.MethodGet, "/mail/config-v1.1.xml", "autoconfig.example.com:8443", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestWellKnownThunderbird(t *testing.T) {
	rec := doRequest(t, testConfig(), http.MethodGet, "/.well-known/autoconfig/mail/config-v1.1.xml", "example.com", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "<domain>example.com</domain>") {
		t.Fatalf("response does not contain domain:\n%s", rec.Body.String())
	}
}

func TestUnknownHost(t *testing.T) {
	for _, tc := range []struct{ method, target string }{
		{http.MethodPost, "/autodiscover/autodiscover.xml"},
		{http.MethodGet, "/mail/config-v1.1.xml"},
	} {
		rec := doRequest(t, testConfig(), tc.method, tc.target, "unknown.test", outlookRequest)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want %d", tc.method, tc.target, rec.Code, http.StatusNotFound)
		}
	}
}

func TestMultipleDomains(t *testing.T) {
	cfg := testConfig()
	cfg.Domains["voronko.de"] = config.Domain{
		IMAP: config.Server{Host: "mail.voronko.de", Port: 993},
		SMTP: config.Server{Host: "mail.voronko.de", Port: 587, STARTTLS: true},
	}

	rec := doRequest(t, cfg, http.MethodGet, "/mail/config-v1.1.xml", "autoconfig.voronko.de", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "<domain>voronko.de</domain>") {
		t.Errorf("response does not contain voronko.de:\n%s", body)
	}
	if !strings.Contains(body, "<hostname>mail.voronko.de</hostname>") {
		t.Errorf("response does not contain voronko mail host:\n%s", body)
	}
}

func TestAppleDisabledByDefault(t *testing.T) {
	rec := doRequest(t, testConfig(), http.MethodGet, "/email.mobileconfig", "autoconfig.example.com", "")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestAppleIgnoresEmailQuery(t *testing.T) {
	cfg := testConfig()
	cfg.EnableApple = true

	rec := doRequest(t, cfg, http.MethodGet, "/email.mobileconfig?email=attacker@evil.test", "autoconfig.example.com", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	assertWellFormedXML(t, body)

	if strings.Contains(body, "attacker@evil.test") {
		t.Fatalf("query email leaked into profile:\n%s", body)
	}
	if !strings.Contains(body, "<string>user@example.com</string>") {
		t.Fatalf("profile does not contain static address:\n%s", body)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/x-apple-aspen-config" {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestThunderbirdResponse(t *testing.T) {
	rec := doRequest(t, testConfig(), http.MethodGet, "/mail/config-v1.1.xml", "autoconfig.example.com", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	assertWellFormedXML(t, body)

	for _, want := range []string{
		`<emailProvider id="example.com">`,
		`<domain>example.com</domain>`,
		`<hostname>imap.example.com</hostname>`,
		`<port>993</port>`,
		`<socketType>SSL</socketType>`,
		`<hostname>smtp.example.com</hostname>`,
		`<port>465</port>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("response does not contain %q:\n%s", want, body)
		}
	}
}

func TestStartTLS(t *testing.T) {
	cfg := testConfig()
	cfg.Domains["example.com"] = config.Domain{
		IMAP: config.Server{Host: "imap.example.com", Port: 143, STARTTLS: true},
		SMTP: config.Server{Host: "smtp.example.com", Port: 587, STARTTLS: true},
	}

	tb := doRequest(t, cfg, http.MethodGet, "/mail/config-v1.1.xml", "autoconfig.example.com", "")
	if !strings.Contains(tb.Body.String(), "<socketType>STARTTLS</socketType>") {
		t.Errorf("thunderbird response has no STARTTLS:\n%s", tb.Body.String())
	}

	ol := doRequest(t, cfg, http.MethodPost, "/autodiscover/autodiscover.xml", "autodiscover.example.com", outlookRequest)
	if !strings.Contains(ol.Body.String(), "<TLS>on</TLS>") {
		t.Errorf("outlook response has no TLS:\n%s", ol.Body.String())
	}
}

func TestHealth(t *testing.T) {
	rec := doRequest(t, testConfig(), http.MethodGet, "/healthz", "example.com", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "ok\n" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestMethodNotAllowed(t *testing.T) {
	rec := doRequest(t, testConfig(), http.MethodDelete, "/mail/config-v1.1.xml", "autoconfig.example.com", "")

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

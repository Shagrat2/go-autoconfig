package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()

	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return p
}

const validDomain = `"example.com": {
	"imap": {"host": "imap.example.com", "port": 993},
	"smtp": {"host": "smtp.example.com", "port": 465}
}`

func TestNewConfig(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name: "valid",
			body: `{"listen": ":8080", "domains": {` + validDomain + `}}`,
		},
		{
			name: "valid pprof loopback",
			body: `{"pprof_listen": "127.0.0.1:6060", "domains": {` + validDomain + `}}`,
		},
		{
			name: "valid pprof localhost",
			body: `{"pprof_listen": "localhost:6060", "domains": {` + validDomain + `}}`,
		},
		{
			name:    "pprof public bind",
			body:    `{"pprof_listen": "0.0.0.0:6060", "domains": {` + validDomain + `}}`,
			wantErr: true,
		},
		{
			name:    "pprof without port",
			body:    `{"pprof_listen": "127.0.0.1", "domains": {` + validDomain + `}}`,
			wantErr: true,
		},
		{
			name:    "no domains",
			body:    `{"listen": ":8080"}`,
			wantErr: true,
		},
		{
			name:    "unknown field",
			body:    `{"listen": ":8080", "domains": {` + validDomain + `}, "extra": true}`,
			wantErr: true,
		},
		{
			name:    "missing imap",
			body:    `{"domains": {"example.com": {"smtp": {"host": "smtp.example.com", "port": 465}}}}`,
			wantErr: true,
		},
		{
			name:    "missing smtp",
			body:    `{"domains": {"example.com": {"imap": {"host": "imap.example.com", "port": 993}}}}`,
			wantErr: true,
		},
		{
			name:    "port zero",
			body:    `{"domains": {"example.com": {"imap": {"host": "imap.example.com", "port": 0}, "smtp": {"host": "smtp.example.com", "port": 465}}}}`,
			wantErr: true,
		},
		{
			name:    "port too high",
			body:    `{"domains": {"example.com": {"imap": {"host": "imap.example.com", "port": 65536}, "smtp": {"host": "smtp.example.com", "port": 465}}}}`,
			wantErr: true,
		},
		{
			name:    "invalid domain",
			body:    `{"domains": {"exa mple.com": {"imap": {"host": "imap.example.com", "port": 993}, "smtp": {"host": "smtp.example.com", "port": 465}}}}`,
			wantErr: true,
		},
		{
			name:    "invalid host",
			body:    `{"domains": {"example.com": {"imap": {"host": "imap.example.com/path", "port": 993}, "smtp": {"host": "smtp.example.com", "port": 465}}}}`,
			wantErr: true,
		},
		{
			name:    "duplicate domain",
			body:    `{"domains": {"Example.com": {"imap": {"host": "imap.example.com", "port": 993}, "smtp": {"host": "smtp.example.com", "port": 465}}, "example.com": {"imap": {"host": "imap.example.com", "port": 993}, "smtp": {"host": "smtp.example.com", "port": 465}}}}`,
			wantErr: true,
		},
		{
			name:    "trailing data",
			body:    `{"domains": {` + validDomain + `}}{}`,
			wantErr: true,
		},
		{
			name:    "not json",
			body:    "listen: :8080",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewConfig(writeConfig(t, tt.body))
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewConfigDefaults(t *testing.T) {
	c, err := NewConfig(writeConfig(t, `{"domains": {`+validDomain+`}}`))
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	if c.Listen != ":8080" {
		t.Errorf("Listen = %q, want %q", c.Listen, ":8080")
	}
	if c.EnableApple {
		t.Error("EnableApple = true, want false")
	}
	if c.PprofListen != "" {
		t.Errorf("PprofListen = %q, want empty", c.PprofListen)
	}
	if _, ok := c.Domains["example.com"]; !ok {
		t.Errorf("domain example.com was not loaded: %v", c.Domains)
	}
}

func TestNewConfigLowercasesDomain(t *testing.T) {
	c, err := NewConfig(writeConfig(t, `{"domains": {"Example.COM": {"imap": {"host": "imap.example.com", "port": 993}, "smtp": {"host": "smtp.example.com", "port": 465}}}}`))
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	if _, ok := c.Domains["example.com"]; !ok {
		t.Errorf("domain was not lowercased: %v", c.Domains)
	}
}

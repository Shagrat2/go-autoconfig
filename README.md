# go-autoconfig
IMAP/SMTP autodiscover feature for Thunderbird, Apple Mail and Microsoft Outlook.

You need DNS SRV-record to get work Outlook and Thunderbird:
```
_autodiscover._tcp IN SRV 0 0 443 autoconfig.example.com.
```
Of course `autoconfig.example.com` domain should point to your server with this service.

### Thunderbird
`GET https://autoconfig.example.com/mail/config-v1.1.xml`

`GET https://example.com/.well-known/autoconfig/mail/config-v1.1.xml`

### Apple Mail
`GET https://autoconfig.example.com/email.mobileconfig` — disabled by default, see `enable_apple`.

The profile contains a static `user@<domain>` address that the user edits after installation.

### Outlook
`POST https://autoconfig.example.com/autodiscover/autodiscover.xml`

### Health
`GET https://autoconfig.example.com/healthz`

## Installation
1. Compile (`go build -o server`).
2. Edit `config.example.json` and copy it to `config.json`.
3. Launch it `./server -config config.json`.
4. Optionally use `nginx` as reverse-proxy and `systemd` to do daemon.

## Configuration
JSON file, one or more domains per instance:

```json
{
  "listen": ":8080",
  "enable_apple": false,
  "domains": {
    "example.com": {
      "imap": {"host": "imap.example.com", "port": 993, "starttls": false},
      "smtp": {"host": "smtp.example.com", "port": 587, "starttls": true}
    },
    "example.org": {
      "imap": {"host": "mail.example.org", "port": 993, "starttls": false},
      "smtp": {"host": "mail.example.org", "port": 587, "starttls": true}
    }
  }
}
```

The domain is selected from the request `Host`: `example.com`, `autoconfig.example.com` and `autodiscover.example.com` resolve to the `example.com` entry. Unknown hosts return `404`.

`listen` defaults to `:8080` and can be overridden with the `AUTOCONFIG_LISTEN` environment variable. Unknown JSON fields are rejected.

### pprof
Disabled by default. Set `pprof_listen` to a loopback address (for example `127.0.0.1:6060`) or set `AUTOCONFIG_PPROF=1` (same address) / `AUTOCONFIG_PPROF=host:port`. The profiler runs on a separate listener; non-loopback binds are rejected. It is never mounted on the public port.

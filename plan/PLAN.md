# План модернизации go-autoconfig

**Fork:** https://github.com/Shagrat2/go-autoconfig  
**Upstream:** https://github.com/savely-krasovsky/go-autoconfig  
**Базовая версия:** upstream `master` (Go 1.18, Echo 4.11.1)  
**Статус:** Phase 1–3 выполнены в ветке `modernize`; аудит протоколов/архитектуры/безопасности — 2026-10-28  
**Дата:** 2026-10-28

---

## 1. Проблема

`go-autoconfig` — небольшой сервис автоконфигурации почты (Outlook Autodiscover, Thunderbird Autoconfig, Apple mobileconfig). Он делает ровно то, что нужно, но текущая реализация устарела и имеет несколько проблем безопасности.

### 1.1. Уязвимость: email из запроса попадает в XML/plist

В `handlers/handlers.go`:

- Outlook: `EMailAddress` читается из тела запроса и передаётся в шаблон.
- Apple: `email` читается из query-параметра и передаётся в шаблон.
- Шаблоны рендерятся через `text/template`, **без экранирования**.

Следствие: специально сформированный email может внедрить XML/plist-разметку (XML/plist injection), а также попасть в access-log (Echo logger логирует query string).

### 1.2. Неограниченное тело запроса

`ioutil.ReadAll(ctx.Request().Body)` в Outlook-обработчике читает тело без лимита — DoS на большом запросе.

### 1.3. Нет фича-флагов

Все маршруты захардкожены в `main.go`:

- `POST /autodiscover/autodiscover.xml`
- `GET /mail/config-v1.1.xml`
- `GET /email.mobileconfig`

«Выключить лишнее настройками» нельзя: Apple-маршрут (с email в query) можно убрать только правкой кода.

### 1.4. Устаревшие зависимости

- `go 1.18`
- `github.com/labstack/echo/v4 v4.11.1`
- `gopkg.in/go-playground/validator.v9 v9.31.0`
- `gopkg.in/yaml.v2 v2.4.0`

По OSV на 2026-10-28: **6 уязвимых модулей, 79 advisories**, в том числе через Echo, `golang.org/x/crypto`, `golang-jwt`. Обновление только директивы `go` это не исправляет.

### 1.5. Один домен на инстанс

Конфиг содержит один `domain`, один IMAP и один SMTP. Для `quantexlab.com` и `voronko.de` нужны либо два инстанса, либо поддержка нескольких доменов в одном конфиге.

### 1.6. Нет тестов, CI и контейнера

Нет golden-тестов ответов, нет `go vet`/`govulncheck` в CI, нет Dockerfile, нет проверки, что сервис не логирует PII.

---

## 2. Цели

1. Убрать приём и отражение email: сервис не должен принимать `?email=` и не должен вставлять email в XML/plist.
2. Перейти на **Go 1.27.3** и **stdlib-only**: `net/http`, `encoding/json`, `html/template`/`encoding/xml`, `embed`.
3. Удалить Echo, `validator.v9`, `yaml.v2` и их транзитивные зависимости.
4. Поддержать несколько доменов одним инстансом (конфиг-мапа).
5. Сделать статический бинарник и минимальный контейнер (non-root, read-only FS).
6. Добавить тесты, golden-файлы, `go vet`, `govulncheck`, CI.
7. Сохранить совместимость с Outlook/Thunderbird; Apple — выключен по умолчанию или статический профиль без email.

## 3. Не цели

- LDAP/AD-поиск.
- Per-user настройки и база данных.
- Динамическая подстановка email в Apple mobileconfig.
- Веб-UI.
- Замена Autodiscover на Exchange.

---

## 4. Решения

| Вопрос | Решение |
|---|---|
| HTTP-стек | `net/http` stdlib; Echo удалить |
| Конфиг | JSON (`encoding/json`), мапа `domains`; env-overrides для listen/health |
| Шаблоны | `html/template` или `encoding/xml`; шаблоны встраиваются через `embed` |
| Email | Не принимаем, не логируем, не отражаем |
| Outlook body | Читаем/отбрасываем с лимитом 1 MiB; email из body не используем |
| Apple | По умолчанию выключен. Если включён — только статический профиль с плейсхолдером, без query-параметра |
| Домены | Один инстанс, `domains` map; домен определяется по `Host` (`autodiscover.*`, `autoconfig.*`, `.well-known`) |
| Модуль | `github.com/Shagrat2/go-autoconfig` |
| Лицензия | MIT (как upstream) |
| Контейнер | multi-stage `golang:1.27.3` → `gcr.io/distroless/static-debian12:nonroot` |
| Ветки | `master` — план; реализация — `modernize`; PR — из `modernize` |

---

## 5. Пошаговый план

### Phase 0 — baseline (сделано)

- [x] Форк `Shagrat2/go-autoconfig`
- [x] Клон в `~/workspace/golang/src/github.com/Shagrat2/go-autoconfig`
- [x] `upstream` remote на `savely-krasovsky/go-autoconfig`
- [x] Этот `PLAN.md`

### Phase 1 — зафиксировать текущее поведение

- [x] Собрать upstream-версию: `go build ./...`
- [x] Запустить с `config.yml`, снять ответы:
  - `POST /autodiscover/autodiscover.xml`
  - `GET /mail/config-v1.1.xml`
  - `GET /email.mobileconfig?email=...`
- [x] Сохранить ответы как golden-файлы в `handlers/testdata/`.
- [x] Добавить тест, демонстрирующий инъекцию:
  - email `x</LoginName><LoginName>evil` в Outlook;
  - email `x</string><key>evil` в Apple.
- [ ] Зафиксировать текущие версии/размер бинарника.

**Deliverable:** тесты/фикстуры, которые показывают проблему и не дают сломать корректные ответы при рефакторинге.

### Phase 2 — security hardening

- [x] Убрать `text/template`; использовать `encoding/xml` (структуры + кастомный plist-маршаллер).
- [ ] Убрать `DisplayName`/`LoginName` с email из Outlook-ответа. Отложено до проверки на Outlook 2016/2019/365: инъекция закрыта экранированием, email остаётся только как данные протокола.
- [x] Убрать `?email=` из Apple-маршрута — параметр игнорируется.
- [x] Apple-маршрут по умолчанию выключен; при включении — статический профиль с `user@<domain>`.
- [x] `http.MaxBytesReader` + лимит 1 MiB на тело.
- [x] Таймауты `http.Server`: `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`.
- [x] Логи без query/body/email: method, path, status, duration, request_id.
- [x] Проверить, что email не попадает в логи.
- [x] Тесты на malicious payload: XML/plist не ломаются, ответ 200/400 по политике.

**Deliverable:** email больше не является входной точкой; нет XML/plist injection; нет unbounded body.

### Phase 3 — переход на stdlib и Go 1.27.3

- [x] `go mod edit -module=github.com/Shagrat2/go-autoconfig -go=1.27.3`
- [x] Удалить `echo`, `validator.v9`, `yaml.v2`; `go mod tidy`.
- [x] `main.go` на `net/http`; `http.ServeMux`; graceful shutdown по SIGTERM.
- [x] Конфиг JSON:

```json
{
  "listen": ":8080",
  "enable_apple": false,
  "domains": {
    "quantexlab.com": {
      "imap": {"host": "mail.quantexlab.com", "port": 993, "starttls": false},
      "smtp": {"host": "mail.quantexlab.com", "port": 587, "starttls": true}
    },
    "voronko.de": {
      "imap": {"host": "mail.voronko.de", "port": 993, "starttls": false},
      "smtp": {"host": "mail.voronko.de", "port": 587, "starttls": true}
    }
  }
}
```

- [x] Валидация конфига вручную: обязательные поля, порты 1..65535, домены без пробелов, unknown fields.
- [x] `embed` шаблонов — не требуется: шаблоны удалены, ответы собираются из структур.
- [x] Определение домена по `Host`: `autodiscover.<d>`, `autoconfig.<d>`, `<d>`; неизвестный домен → 404.
- [x] Маршруты:
  - `POST /autodiscover/autodiscover.xml`
  - `GET /autodiscover/autodiscover.xml`
  - `GET /mail/config-v1.1.xml`
  - `GET /.well-known/autoconfig/mail/config-v1.1.xml`
  - `GET /healthz`
  - `GET /email.mobileconfig` — только если `enable_apple=true`
- [x] `config.example.json`, `README` с примером запуска.
- [x] `go list -m all` — только собственный модуль.

**Deliverable:** один статический бинарник без сторонних Go-зависимостей.

### Phase 4 — тесты и совместимость

- [x] Unit-тесты: определение домена по `Host`, валидация конфига, неизвестный домен, лимит body.
- [x] Golden-тесты Outlook/Thunderbird/Apple (`handlers/testdata/`).
- [ ] Fuzz-тест `Host` и Outlook body: `go test -fuzz=Fuzz -fuzztime=30s`.
- [x] `go vet ./...`, `staticcheck ./...` — clean.
- [x] `govulncheck ./...` — 0 findings.
- [ ] Ручная матрица:
  - Outlook 2016/2019/365 desktop: `Test E-mail AutoConfiguration`.
  - Thunderbird: autoconfig.
  - Apple Mail/iOS: профиль (если включён) или ручная настройка.
  - Android K-9/FairEmail: autoconfig.
- [ ] Проверить оба домена и TLS-сертификаты `autoconfig.*`/`autodiscover.*`.
- [ ] Зафиксировать результаты в `docs/compat.md`.

**Deliverable:** доказательства совместимости и отсутствия известных уязвимостей.

### Phase 5 — упаковка и CI

- [ ] `Dockerfile`:

```dockerfile
FROM golang:1.27.3 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/autoconfig .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/autoconfig /autoconfig
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/autoconfig", "-config", "/etc/autoconfig/config.json"]
```

- [ ] `.dockerignore`.
- [ ] GitHub Actions: `go test`, `go vet`, `govulncheck`, `docker build`.
- [ ] Dependabot для Go modules и Actions.
- [ ] `SECURITY.md` с политикой отчетности.
- [ ] SBOM (`go version -m` / syft) — опционально.

**Deliverable:** воспроизводимый образ и CI, который ловит регрессии.

### Phase 6 — деплой и переключение

- [ ] Собрать `registry.quantexlab.com/autodiscover-go:<tag>`; проверить `docker run` локально.
- [ ] Манифесты k8s для двух доменов (или одного инстанса с `domains` map).
- [ ] Ingress: `autoconfig.quantexlab.com`, `autodiscover.quantexlab.com`, `autoconfig.voronko.de`, `autodiscover.voronko.de`.
- [ ] DNS/TLS: CNAME/A + сертификаты; SRV `_autodiscover._tcp` опционально.
- [ ] Canary: один домен, затем второй.
- [ ] Проверить Outlook/Thunderbird/Apple на реальных клиентах.
- [ ] Rollback: вернуть старый образ/манифест.
- [ ] Удалить старый Node/Autodiscover после успешного переключения.

**Deliverable:** новый сервис в проде, старый выключен, есть rollback.

### Phase 7 — PR в upstream

- [ ] Ветка `modernize` от `master`.
- [ ] Отдельные коммиты: security fixes, stdlib migration, tests, packaging.
- [ ] PR description:
  - проблема (email injection, body limit, EOL deps);
  - что изменено;
  - как проверено (`go test`, `govulncheck`, compat);
  - breaking changes/конфиг;
  - ссылка на этот план.
- [ ] Не включать секреты, внутренние домены и токены.
- [ ] Если upstream не принимает stdlib-переписывание — предложить поэтапно: сначала security fixes, потом миграцию.

**Deliverable:** PR, готовый к ревью.

---

## 6. Критерии готовности

- [x] Apple не принимает `email` из query; email из Outlook-body только экранируется и не попадает в лог (удаление отложено, см. Phase 2).
- [x] Нет `text/template` для пользовательских данных.
- [x] Тело запроса ограничено; таймауты заданы.
- [x] `go list -m all` — только `github.com/Shagrat2/go-autoconfig`.
- [x] `go test ./...` — green.
- [x] `go vet ./...` — clean.
- [ ] `govulncheck ./...` — 0 findings.
- [x] Outlook/Thunderbird отвечают корректно; Apple выключен или статический.
- [x] Два домена работают одним инстансом.
- [ ] Образ non-root, read-only, без shell.
- [x] Логи не содержат email/query/body.
- [x] Есть README и пример конфига.

---

## 7. Риски и открытые вопросы

1. **Outlook и `LoginName`.** Если Outlook откажется работать без `LoginName`, вернуть его можно только с проверкой домена и XML-escaping. Проверить на 2016/2019/365.
2. **Apple mobileconfig.** Статический профиль с `user@domain` может требовать ручной правки email. Если нужен автоматический email — только отдельный endpoint с аутентификацией/allowlist, но это противоречит выбранному курсу.
3. **Кэш Autodiscover.** Клиенты кэшируют настройки; при переключении может понадобиться очистка кэша/перезапуск Outlook.
4. **DNS/TLS.** Без валидных сертификатов на `autoconfig.*`/`autodiscover.*` клиенты не будут доверять ответам.
5. **Новый Outlook/Outlook mobile.** Может требовать Autodiscover v2 JSON; проверить отдельно, при необходимости добавить статический JSON.
6. **Upstream.** Проект маленький и давно не обновлялся; PR может потребовать обсуждения. Держать изменения небольшими и по этапам.

---

## 8. Быстрые команды

```bash
# сборка/тесты
go build ./...
go test ./...
go vet ./...
govulncheck ./...

# локальный запуск
go run . -config config.example.json
curl -i -X POST http://127.0.0.1:8080/autodiscover/autodiscover.xml \
  -H 'Content-Type: text/xml' \
  --data-binary @testdata/autodiscover-request.xml
curl -i http://127.0.0.1:8080/mail/config-v1.1.xml

# контейнер
docker build -t autodiscover-go:dev .
docker run --rm -p 8080:8080 -v "$PWD/config.example.json:/etc/autoconfig/config.json:ro" autodiscover-go:dev
```

---

## 9. Ссылки

- Upstream: https://github.com/savely-krasovsky/go-autoconfig
- Fork: https://github.com/Shagrat2/go-autoconfig
- OSV: https://osv.dev
- Go 1.27.3: https://go.dev/dl/
- Mozilla Autoconfig: https://wiki.mozilla.org/Thunderbird:Autoconfiguration
- Microsoft Autodiscover: https://learn.microsoft.com/exchange/autodiscover/outlook/responseschema/2006a
- Apple mobileconfig: https://developer.apple.com/documentation/devicemanagement/email

---

## 10. Аудит 2026-10-28: протоколы, архитектура, безопасность

Источники: MS-OXDSCLI (Autodiscover response XSD и AcceptableResponseSchema), MS Learn «Autodiscover for Exchange» и «Autodiscover service in Exchange Server», MozillaWiki Thunderbird Autoconfiguration / ConfigFileFormat / Security, Apple Device Management «Mail», RFC 6186, RFC 8314.

Обозначения: `[x]` — сделано, `[ ]` — todo. Приоритеты: **P0** — блокер выката, **P1** — до прода, **P2** — следующий спринт, **P3** — улучшение.

### 10.1. P0 — блокеры выката

- [ ] Dockerfile: multi-stage `golang:1.27.3` → `gcr.io/distroless/static-debian12:nonroot`; `.dockerignore`; пиннинг базовых образов по digest.
- [ ] Контейнер: non-root, read-only rootfs, `cap_drop: ALL`, `no-new-privileges`, без shell, лимиты CPU/RAM.
- [ ] CI (GitHub Actions): `go build`, `go test`, `go vet`, `staticcheck`, `govulncheck`, `docker build`; Dependabot для Go и Actions.
- [ ] Supply chain: SBOM (syft или `go version -m`), подпись образа (cosign), provenance.
- [ ] TLS-терминация + HSTS; HTTP→HTTPS redirect для `http://autodiscover.<domain>/autodiscover/autodiscover.xml`; валидные сертификаты на `autodiscover.*`, `autoconfig.*` и bare-domain.
- [ ] DNS: CNAME/A, `_autodiscover._tcp`, `_imaps._tcp`, `_submission._tcp`/`_submissions._tcp`; при возможности DNSSEC и TLSA (RFC 6186/8314).
- [ ] Ручная матрица совместимости: Outlook 2016/2019/365, Thunderbird, Apple Mail/iOS (с `enable_apple`), K-9/FairEmail; результаты в `docs/compat.md`.

### 10.2. P1 — протокольная корректность и безопасность

- [ ] Apple: `IncomingMailServerUseSSL`/`OutgoingMailServerUseSSL` = `!STARTTLS`; сейчас жёстко `<true/>` (`handlers/responses.go:281,286`), из-за чего профиль для 587/STARTTLS неверен. Проверить на iOS/macOS.
- [ ] Autodiscover: неизвестный `AcceptableResponseSchema` → `400`; сейчас молча подставляется `2006a` (`allowlistedSchema`). По MS-OXDSCLI клиент MUST присылать `.../outlook/responseschema/2006a`; пустой — можно трактовать как `2006a`.
- [ ] Autodiscover `User`: добавить `AutoDiscoverSMTPAddress` (и решить по `LegacyDN`/`DeploymentId`) либо зафиксировать отклонение от XSD; проверить на Outlook 2016/2019/365.
- [ ] Rate limiting: token bucket на IP или лимиты на ingress для `/autodiscover/*` и `/mail/*`; защита от флуда и усиления памяти.
- [ ] `X-Request-ID`: не доверять клиентскому значению без проверки; принимать только `^[A-Za-z0-9._-]{1,64}$` либо всегда генерировать; ответный заголовок сохранить.
- [ ] Логировать ошибки рендеринга (`marshalXML`/`marshalPlist`) с `request_id`; клиенту — только `500 internal server error`.
- [x] `http.Server.MaxHeaderBytes` = 16 KiB для основного и pprof-listener.
- [ ] `Cache-Control: no-store` для Outlook-ответа с email.
- [ ] Заголовки безопасности: HSTS на прокси, `X-Content-Type-Options: nosniff` уже есть; добавить `Referrer-Policy: no-referrer` на случай ошибок.

### 10.3. P2 — полнота протоколов, диагностика, наблюдаемость

- [x] **pprof**: отдельный admin-listener `pprof_listen` (по умолчанию выключен; `AUTOCONFIG_PPROF=1` → `127.0.0.1:6060`), отдельный `http.Server` с таймаутами и `MaxHeaderBytes`; non-loopback bind отвергается; в публичный mux не монтируется; тесты на admin 200 и public 404; README обновлён.
- [ ] Autodiscover v2 JSON (`/autodiscover/autodiscover.json`): исследовать официальную схему и, если нужна новым Outlook/mobile, отдавать статический JSON (Protocol/AccountType/Authentication/EmailAddress/Host/Port/SSL/Username).
- [ ] Apple payload: `SMIMEEncryptionEnabled`, `SMIMESigningEnabled`, `allowMailDrop`; убрать не входящий в схему `HasRemovalPasscode`; `PayloadVersion` внутреннего payload — integer, не real; `PayloadUUID`/`PayloadIdentifier` детерминированно на домен (сейчас статические константы для всех доменов).
- [ ] Thunderbird: конфигурируемые `socketType` (`SSL`/`STARTTLS`, при необходимости `plain`) и `authentication` (сейчас только `password-cleartext`).
- [ ] Брендинг на домен: `displayName`/`displayShortName` Thunderbird, `PayloadOrganization`/`EmailAccountDescription` Apple; сейчас подставляется `%EMAILADDRESS%`/домен.
- [ ] Fuzz-тесты: `Host`, Outlook XML body, plist-поля; `go test -fuzz=Fuzz -fuzztime=30s`.
- [ ] Наблюдаемость: `/version` (`debug.ReadBuildInfo`), счётчики запросов/ошибок (Prometheus) — опционально; логирование 5xx.
- [ ] При добавлении метрик на основной mux реализовать `http.Flusher`/`Hijacker` в `statusRecorder` (pprof уже на отдельном listener).
- [ ] Runbook по DNS/TLS и приватности: какие SRV/CNAME, TLS 1.2+, сроки хранения логов.

### 10.4. P3 — улучшения

- [ ] Hot reload конфига по SIGHUP (сейчас только рестарт).
- [ ] SOAP Autodiscover (`.svc`) — явный `400` и документация «не поддерживается» (сейчас 404).
- [ ] Подписанные Apple-профили (сейчас unsigned; установка даёт предупреждение).
- [ ] `socketType=plain` для Thunderbird (не рекомендуется RFC 8314).
- [ ] Бенчмарки рендеринга и маршрутизации.
- [ ] Единый формат ошибок (`application/problem+json`) — только если появится API.

### 10.5. Соответствие протоколам

#### Outlook Autodiscover (POX, MS-OXDSCLI)

| # | Требование | Статус |
|---|---|---|
| 1 | `https://<domain>/autodiscover/autodiscover.xml` и `https://autodiscover.<domain>/autodiscover/autodiscover.xml` | [x] Host-routing + case-insensitive path |
| 2 | POX POST с `EMailAddress` и `AcceptableResponseSchema` | [x] |
| 3 | `AcceptableResponseSchema` = `.../outlook/responseschema/2006a` | [x] allowlist 2006a/2006; [ ] не-allowlisted → 400 (P1) |
| 4 | Root `xmlns=.../responseschema/2006`, `Response xmlns=<schema>` | [x] |
| 5 | `User/DisplayName` | [x] |
| 6 | `User/LegacyDN`, `AutoDiscoverSMTPAddress`, `DeploymentId` (XSD) | [ ] `AutoDiscoverSMTPAddress` + решение по остальным (P1) |
| 7 | `Account/AccountType=email`, `Action=settings` | [x] |
| 8 | `Protocol`: Type, TTL, Server, Port, LoginName, DomainRequired, DomainName, SPA, TLS/SSL, AuthRequired | [x] |
| 9 | Unauthenticated GET → `302` на HTTPS (phase 3 MS) | [ ] (P2; сейчас 400, если прокси не редиректит) |
| 10 | Autodiscover v2 JSON | [ ] исследование/реализация (P2) |
| 11 | SOAP `.svc` | N/A; [ ] явный 400 + docs (P3) |
| 12 | HTTPS + доверенный сертификат | [ ] деплой (P0) |

#### Thunderbird (Mozilla ConfigFileFormat)

| # | Требование | Статус |
|---|---|---|
| 1 | `GET /mail/config-v1.1.xml` | [x] |
| 2 | `GET /.well-known/autoconfig/mail/config-v1.1.xml` | [x] |
| 3 | `?emailaddress=` опционален, не обязателен | [x] игнорируется, не логируется; [ ] тест (P3) |
| 4 | `clientConfig version=1.1`, `emailProvider id`, `domain` | [x] |
| 5 | `displayName`/`displayShortName` с плейсхолдерами | [x] |
| 6 | `incomingServer`/`outgoingServer`: type, hostname, port, socketType, authentication, username | [x] |
| 7 | `socketType`: `SSL`/`STARTTLS`/`plain` | [x] SSL/STARTTLS; [ ] plain (P3, discouraged) |
| 8 | `authentication`: password-cleartext и др. | [x] password-cleartext; [ ] конфигурируемость (P2) |
| 9 | HTTPS + проверка домена/сертификата | [ ] деплой (P0) |
| 10 | SRV `_imaps._tcp`, `_submission(s)._tcp` (RFC 6186/8314) | [ ] runbook (P2) |

#### Apple Mail (Device Management, Mail payload)

| # | Требование | Статус |
|---|---|---|
| 1 | `PayloadType=com.apple.mail.managed`, `EmailTypeIMAP` | [x] |
| 2 | Обязательные: account type, user display name, email, host, port, username, auth | [x] |
| 3 | `Use SSL` соответствует implicit TLS | [ ] `!STARTTLS` (P1) |
| 4 | Опциональные S/MIME и MailDrop ключи | [ ] (P2) |
| 5 | `PayloadVersion` — integer | [ ] сейчас `<real>` (P2) |
| 6 | Уникальные `PayloadUUID`/`PayloadIdentifier` на домен | [ ] сейчас статические (P2) |
| 7 | Отсутствие не входящего в схему `HasRemovalPasscode` | [ ] (P2) |
| 8 | `Content-Type: application/x-apple-aspen-config` + attachment | [x] |
| 9 | Подпись профиля | [ ] (P3) |
| 10 | Статический `user@<domain>` | [x], требует ручной правки; альтернатива — MDM |

#### TLS и SRV (RFC 6186 / RFC 8314)

- [x] Поддержка implicit TLS и STARTTLS в конфиге.
- [ ] Документировать приоритет implicit TLS (993/465) над STARTTLS (RFC 8314).
- [ ] Требование TLS 1.2+ и валидных сертификатов на mail-серверах — в runbook (P0).
- [ ] SRV-записи и DNSSEC/TLSA — в runbook (P2).

### 10.6. Архитектурный обзор (`archreview`)

- [x] Обзор выполнен.
- [x] Слои `main` (lifecycle/middleware) → `handlers` (routing/domain/render) → `config` (parse/validate) адекватны размеру; дробление преждевременно.
- [x] Zero-dependency, статический бинарник, graceful shutdown, типизированный рендеринг — хорошая база.
- [ ] Вынести `newServer(cfg)`/middleware в тестируемый конструктор (сейчас `run()` в `package main`) — P3.
- [x] `statusRecorder` не реализует `http.Flusher`/`Hijacker`; pprof вынесен на отдельный listener, поэтому риск снят. Для метрик на основном mux — P2.
- [ ] 5xx рендеринга не логируются — P1.
- [ ] Нет метрик/версии/build info — P2/P3.
- [ ] Нет rate limiting — P1.
- [ ] Нет hot reload конфига — P3 (документировать рестарт).
- [ ] Нет Dockerfile/CI — P0.

### 10.7. Безопасность по регламентам

| Регламент | Пункт | Статус |
|---|---|---|
| OWASP Top 10 2021 | A03 Injection: XML/plist escaping, allowlist | [x] |
| OWASP Top 10 2021 | A03 Injection: fuzz Host/body/plist | [ ] P2 |
| OWASP Top 10 2021 | A04 Insecure design: body limit, timeouts | [x] |
| OWASP Top 10 2021 | A04 Insecure design: rate limiting | [ ] P1 |
| OWASP Top 10 2021 | A05 Misconfig: строгий JSON, Apple off by default | [x] |
| OWASP Top 10 2021 | A05 Misconfig: HSTS/security headers/TLS | [ ] P0/P2 |
| OWASP Top 10 2021 | A06 Vulnerable components: zero deps + govulncheck | [x] |
| OWASP Top 10 2021 | A06/A08: CI, Dependabot, SBOM, signing | [ ] P0 |
| OWASP Top 10 2021 | A09 Logging: без PII, request_id | [x] |
| OWASP Top 10 2021 | A09 Logging: мониторинг/алерты/retention | [ ] P2 |
| OWASP Top 10 2021 | A10 SSRF: исходящих запросов нет | [x] |
| OWASP ASVS 4.0 | V5 validation, V6 encoding, V14 config | [x] |
| OWASP ASVS 4.0 | V7 errors/logging (5xx не логируются) | [ ] P1 |
| OWASP ASVS 4.0 | V9 communications (TLS/HSTS/сертификаты) | [ ] P0 |
| OWASP ASVS 4.0 | V14 config: pprof только loopback, отдельный listener | [x] |
| Go security | govulncheck, staticcheck, timeouts, MaxBytesReader, crypto/rand, без unsafe | [x] |
| Go security | fuzz-тесты парсеров | [ ] P2 |
| CIS Docker Benchmark | non-root, read-only, cap_drop, no shell, limits | [ ] P0 |
| SLSA / supply chain | пин toolchain, пустой граф зависимостей | [x] |
| SLSA / supply chain | SBOM, provenance, cosign, digest-пиннинг | [ ] P0 |
| RFC 8314 | TLS 1.2+, приоритет implicit TLS | [ ] P0 (TLS) / P2 (docs) |
| Mozilla Autoconfig Security | HTTPS + проверка сертификата | [ ] P0 |
| Privacy/GDPR | нет хранения данных, нет PII в логах | [x] |
| Privacy/GDPR | retention/политика логов | [ ] P2 |
| Microsoft Autodiscover | HTTPS + доверенный сертификат до отправки учётных данных | [ ] P0 |

### 10.8. Автоматизация матрицы клиентов

Готового сервиса, который прогонит всю матрицу «клиент × ОС», нет. Часть проверок автоматизируется официальными/community-инструментами, часть остаётся ручной.

- [x] Инвентаризация инструментов:
  - **Microsoft Remote Connectivity Analyzer** (`testconnectivity.microsoft.com`) — Outlook Autodiscover/Outlook Connectivity, внешняя проверка DNS/SRV/HTTP/сертификата/XML. Ограничение: Exchange-ориентирован, для IMAP-провайдера покажет транспорт и XML, но не финальное принятие настроек.
  - **MXToolbox Autodiscover/SRV** — DNS-слой (CNAME/A/SRV), не полный flow.
  - **SSL Labs / testssl.sh / openssl s_client** — TLS-версии, цепочка, SNI, срок действия.
  - **`xmllint --schema` + MS-OXDSCLI XSD** — структурный конформанс Outlook POX.
  - **`plutil -lint` + проверка обязательных ключей** — Apple plist.
  - **Thunderbird ISPDB `tools/validate.py`** (`github.com/thunderbird/autoconfig`) — только well-formedness XML, не схема.
  - **Android emulator + Appium/UiAutomator** — K-9/FairEmail.
  - **Apple Configurator 2 / Simulator (`xcrun simctl`)** — установка профиля; реальное подключение Mail проверяется вручную.
  - **Self-hosted Windows/macOS runner** — Outlook desktop и Apple Mail; требует лицензий и обслуживания.
- [ ] Vendoring/адаптация MS-OXDSCLI XSD + CI-step `xmllint --schema`. Строгий XSD Exchange-ориентирован (требует `LegacyDN`, `AutoDiscoverSMTPAddress`, `DeploymentId`, `MicrosoftOnline`), поэтому либо добавить обязательные поля, либо вести отдельный IMAP-профиль XSD с зафиксированными отклонениями.
- [ ] CI: `plutil`/`lxml` для Apple и Thunderbird, HTTP contract-тесты (Host/status/headers/body limit), nightly DNS/SRV/TLS-проверки staging.
- [ ] Thunderbird: headless-профиль + UI-автоматизация (Marionette или аналог) с проверкой итоговых настроек в `prefs.js`; при невозможности — один ручной прогон на релиз.
- [ ] Android: emulator + Appium для K-9/FairEmail (опционально, если нужна регулярная проверка).
- [ ] RCA: регламент прогона перед релизом (Outlook Autodiscover + Outlook Connectivity), результаты — в `docs/compat.md`.
- [ ] Минимальный ручной smoke на релиз: Outlook desktop, Thunderbird, iOS/macOS — ~30 минут вместо полной матрицы.

### 10.9. Критерии закрытия аудита

- [ ] Все P0 закрыты; образ и CI воспроизводимы.
- [ ] Apple STARTTLS-профиль проверен на iOS/macOS.
- [ ] Outlook 2016/2019/365 принимает ответ без ручных правок.
- [x] pprof доступен только на loopback/admin-listener.
- [ ] `govulncheck`/`staticcheck`/`go test -race` зелёные в CI.
- [ ] Runbook по TLS/DNS/логам и `docs/compat.md` заполнены.

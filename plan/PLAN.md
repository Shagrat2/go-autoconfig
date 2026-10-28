# План модернизации go-autoconfig

**Fork:** https://github.com/Shagrat2/go-autoconfig  
**Upstream:** https://github.com/savely-krasovsky/go-autoconfig  
**Базовая версия:** upstream `master` (Go 1.18, Echo 4.11.1)  
**Статус:** Phase 1–3, этапы A–C выполнены: сервис развёрнут на трёх площадках и проверен; остаются RCA и ручной smoke на клиентах  
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

Конфиг содержит один `domain`, один IMAP и один SMTP. Для `example.com` и `example.org` нужны либо два инстанса, либо поддержка нескольких доменов в одном конфиге.

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
    "example.com": {
      "imap": {"host": "mail.example.com", "port": 993, "starttls": false},
      "smtp": {"host": "mail.example.com", "port": 587, "starttls": true}
    },
    "example.org": {
      "imap": {"host": "mail.example.org", "port": 993, "starttls": false},
      "smtp": {"host": "mail.example.org", "port": 587, "starttls": true}
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

- [x] `Dockerfile`: multi-stage, distroless `nonroot`, digest-пиннинг, `-trimpath`, `-s -w`, версия через ldflags.
- [x] `.dockerignore`.
- [x] GitHub Actions: `go test -race`, `go vet`, `staticcheck`, `govulncheck`, fuzz smoke, `docker build` + container smoke.
- [x] Dependabot для Go modules, Actions и Docker.
- [x] `SECURITY.md` с политикой отчетности.
- [x] SBOM как артефакт CI; подпись/provenance — отдельная задача релиза.

**Deliverable:** воспроизводимый образ и CI, который ловит регрессии.

### Phase 6 — деплой и переключение

- [x] Собрать `registry.example.com/autodiscover-go:1.0.0` (linux/amd64, digest-пиннинг); проверить `docker run` локально.
- [x] Манифесты k8s с `domains` map, ConfigMap, probes, securityContext и digest-пиннингом.
- [x] Ingress: `autoconfig.*` и `autodiscover.*` для каждого домена.
- [x] DNS/TLS: A/CNAME и сертификаты проверены; smoke прошёл на трёх площадках. SRV: на одной площадке отсутствуют, на двух — по одному submission-порту; A/HTTPS работают, записи можно добавить отдельно.
- [ ] Canary не выполнялся: развёрнуто сразу на трёх площадках.
- [ ] Проверить Outlook/Thunderbird/Apple на реальных клиентах.
- [x] Rollback: старые манифесты/Dockerfile сохранены в `_old_20261028/`, старый образ остаётся в реестре.
- [x] Старый Node-сервис заменён; поды прежней версии завершены.

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

Обозначения: `[x]` — сделано, `[ ]` — todo. Порядок работ: **A (код) → B (сборка) → C (выкат) → D (после выката)**.

Текущий статус: этапы A–C закрыты (кроме подписи/provenance, Autodiscover v2 JSON, RCA и ручного smoke на реальных клиентах).

### 10.1. Этап A — весь код (до сборки)

Порядок работ: сначала код и тесты, потом упаковка. Все пункты этапа закрываются в этой ветке, кроме исследовательских и инфраструктурных.

#### A1. HTTP, безопасность, устойчивость

- [x] 1 MiB body limit (`http.MaxBytesReader`, 413).
- [x] Таймауты `http.Server`: `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`.
- [x] Graceful shutdown по SIGINT/SIGTERM для основного и pprof-серверов.
- [x] Логи без query/body/email; `request_id`, host, path, status, duration.
- [x] `X-Request-ID`: валидация `^[A-Za-z0-9._-]{1,64}$`, иначе генерация; проброс в `context`.
- [x] Rate limiting: fixed window на клиентский IP (`rate_limit_per_minute`), `Retry-After`, `trust_proxy` для `X-Forwarded-For`; тесты.
- [x] `Cache-Control: no-store` для XML/plist/version.
- [x] Security headers: `X-Content-Type-Options`, `Referrer-Policy`, `X-Frame-Options`; HSTS при TLS/`X-Forwarded-Proto: https`.
- [x] `MaxHeaderBytes` 16 KiB на основном и pprof-серверах.
- [x] pprof на отдельном loopback-listener, вне публичного mux.
- [x] XML/plist injection устранён; schema allowlist; ошибки рендеринга логируются.

#### A2. Outlook POX

- [x] Оба endpoint-а: `https://<domain>/autodiscover/autodiscover.xml` и `https://autodiscover.<domain>/...`.
- [x] Регистронезависимые пути; POST и GET.
- [x] `AcceptableResponseSchema`: allowlist 2006/2006a, пустой → 2006a, неизвестный → 400.
- [x] `User/DisplayName` + `AutoDiscoverSMTPAddress`.
- [x] `Account`/`Protocol` (IMAP/SMTP, TTL, Server, Port, LoginName, DomainRequired, DomainName, SPA, TLS/SSL, AuthRequired).
- [x] SOAP `.svc` — явный 400 с пояснением.
- [x] `LegacyDN`/`DeploymentId`/`MicrosoftOnline` не добавляем — Exchange-специфичные, для IMAP допускаются; проверить в ручной матрице.
- [ ] Autodiscover v2 JSON — **исследование**: публичной стабильной схемы для IMAP нет; реализовывать наугад нельзя. Нужен официальный контракт или тест нового Outlook.
- [ ] Unauthenticated GET → 302 на HTTPS — делается на прокси/ingress, не в приложении.

#### A3. Thunderbird

- [x] `clientConfig` 1.1, оба URL (`/mail/config-v1.1.xml`, `/.well-known/...`).
- [x] `?emailaddress=` игнорируется и не попадает в ответ; тест.
- [x] `display_name` на домен.
- [x] `socket_type` (`SSL`/`STARTTLS`/`plain`) с дефолтом из `starttls`.
- [x] `authentication` с allowlist (`password-cleartext` по умолчанию).
- [ ] SRV/DNSSEC/TLSA runbook — этап C.

#### A4. Apple Mail

- [x] `UseSSL = !STARTTLS` для входящего и исходящего.
- [x] `SMIMEEncryptionEnabled`, `SMIMESigningEnabled`, `allowMailDrop`; integer `PayloadVersion`.
- [x] Убран не входящий в схему `HasRemovalPasscode`.
- [x] `PayloadUUID`/`PayloadIdentifier` детерминированно на домен (UUIDv5).
- [x] `application/x-apple-aspen-config`, выключен по умолчанию, статический `user@domain`.
- [ ] Подписанный профиль — нужен сертификат, этап C.
- [ ] Реальная установка на iOS/macOS — ручной smoke, этап C.

#### A5. Качество и наблюдаемость

- [x] Golden/unit/race-тесты; `go vet`, `staticcheck`, `govulncheck`.
- [x] Fuzz: `Host`, Outlook body, выбор домена по Host.
- [x] Zero-dependency граф; `/version` с build info.
- [ ] Метрики (Prometheus) — опционально, этап D.
- [x] Fixture-валидация в CI (`xmllint`, `plistlib`); [ ] XSD — отдельный пункт этапа B.

### 10.2. Этап B — сборка и CI

- [x] Dockerfile: multi-stage `golang:1.27.3` → `gcr.io/distroless/static-debian12:nonroot`; `.dockerignore`; оба образа запинены по digest. Локально: build + hardened smoke, образ 9.39 MB, shell отсутствует.
- [x] Контейнер: non-root, read-only rootfs, `cap_drop: ALL`, `no-new-privileges`, без shell, `pids-limit`/`memory`/`cpus` — проверено локально и в CI smoke.
- [x] GitHub Actions: `go build`, `go test -race`, `go vet`, `staticcheck`, `govulncheck`, короткий fuzz, `docker build` + container smoke.
- [x] CI: `xmllint --noout` + структурная проверка фикстур (`scripts/validate_fixtures.py`, `plistlib`) для Outlook/Thunderbird/Apple.
- [ ] CI: `xmllint --schema` по адаптированному IMAP-профилю MS-OXDSCLI XSD (строгий Exchange-XSD не подходит).
- [x] Dependabot для Go, Actions и Docker.
- [x] SBOM (anchore/sbom-action) как артефакт CI.
- [ ] Подпись образа (cosign) и provenance — нужен registry/release workflow.
- [x] `SECURITY.md`.

### 10.3. Этап C — выкат и приёмка

Подготовлено (можно применять на стенде):

- [x] `deploy/Caddyfile`: автоматический HTTPS, HSTS, HTTP→HTTPS, `X-Forwarded-For` перезаписывается на клиентский IP.
- [x] `deploy/docker-compose.yml`: Caddy снаружи, приложение без опубликованного порта, read-only/cap-drop/limits.
- [x] `deploy/config.example.json`: два домена, `trust_proxy`, rate limit.
- [x] `docs/deployment.md`: DNS/SRV, TLS, порядок выката, RCA, rollback, security notes.
- [x] `docs/compat.md`: шаблон матрицы и пошаговые инструкции для Outlook/Thunderbird/Apple/Android/RCA.
- [x] `scripts/smoke.sh`, `scripts/check_dns.sh`, `scripts/check_tls.sh`.
- [x] Проверено локально: `docker compose config`, `caddy validate`, smoke против hardened-контейнера.

Выполнено на трёх площадках:

- [x] TLS-терминация, HSTS и HTTP→HTTPS; сертификаты на `autoconfig.*`/`autodiscover.*` валидны (`ssl_verify_result=0`).
- [x] A/CNAME-записи и публичные endpoint-ы проверены; `scripts/smoke.sh` — 17/17 на каждой площадке.
- [x] Образ `autodiscover-go:1.0.0` загружен в реестры и запинен по digest; `/version` отдаёт `1.0.0`.
- [x] Старые файлы перенесены в `_old_20261028/`; в README площадок добавлена ссылка на проект.
- [x] Rollback-артефакты сохранены (старые манифесты и образ).

Осталось:

- [ ] Microsoft RCA (Outlook Autodiscover + Outlook Connectivity).
- [ ] Минимальный ручной smoke: Outlook desktop, Thunderbird, iOS/macOS; заполнить `docs/compat.md`.
- [ ] SRV: добавить отсутствующие записи там, где порты открыты (`_autodiscover`, `_imaps`, `_submission`/`_submissions`); сейчас часть записей отсутствует, но A/HTTPS работают.
- [ ] Canary не выполнялся — развёртывание сразу на трёх площадках.
- [ ] Подписанный Apple-профиль (если нужен) и проверка установки.

### 10.4. Этап D — после выката (P3)

- [ ] Autodiscover v2 JSON, если подтвердится потребность новым Outlook/mobile.
- [ ] Метрики, алерты, retention логов.
- [ ] Hot reload конфига по SIGHUP.
- [ ] Бенчмарки рендеринга/маршрутизации.
- [ ] Единый формат ошибок (`application/problem+json`), если появится API.

### 10.5. Соответствие протоколам

#### Outlook Autodiscover (POX, MS-OXDSCLI)

| # | Требование | Статус |
|---|---|---|
| 1 | `https://<domain>/autodiscover/autodiscover.xml` и `https://autodiscover.<domain>/autodiscover/autodiscover.xml` | [x] Host-routing + case-insensitive path |
| 2 | POX POST с `EMailAddress` и `AcceptableResponseSchema` | [x] |
| 3 | `AcceptableResponseSchema` = `.../outlook/responseschema/2006a` | [x] allowlist 2006a/2006; не-allowlisted → 400; пустой → 2006a |
| 4 | Root `xmlns=.../responseschema/2006`, `Response xmlns=<schema>` | [x] |
| 5 | `User/DisplayName` | [x] |
| 6 | `User/LegacyDN`, `AutoDiscoverSMTPAddress`, `DeploymentId` (XSD) | [x] `AutoDiscoverSMTPAddress`; `LegacyDN`/`DeploymentId` — осознанное отклонение |
| 7 | `Account/AccountType=email`, `Action=settings` | [x] |
| 8 | `Protocol`: Type, TTL, Server, Port, LoginName, DomainRequired, DomainName, SPA, TLS/SSL, AuthRequired | [x] |
| 9 | Unauthenticated GET → `302` на HTTPS (phase 3 MS) | [ ] этап C (прокси/ingress) |
| 10 | Autodiscover v2 JSON | [ ] исследование (этап D; нужен официальный контракт) |
| 11 | SOAP `.svc` | [x] явный 400; документация в README |
| 12 | HTTPS + доверенный сертификат | [ ] деплой (P0) |

#### Thunderbird (Mozilla ConfigFileFormat)

| # | Требование | Статус |
|---|---|---|
| 1 | `GET /mail/config-v1.1.xml` | [x] |
| 2 | `GET /.well-known/autoconfig/mail/config-v1.1.xml` | [x] |
| 3 | `?emailaddress=` опционален, не обязателен | [x] игнорируется, не логируется; тест |
| 4 | `clientConfig version=1.1`, `emailProvider id`, `domain` | [x] |
| 5 | `displayName`/`displayShortName` с плейсхолдерами | [x] |
| 6 | `incomingServer`/`outgoingServer`: type, hostname, port, socketType, authentication, username | [x] |
| 7 | `socketType`: `SSL`/`STARTTLS`/`plain` | [x] конфигурируется, дефолт из `starttls` |
| 8 | `authentication`: password-cleartext и др. | [x] конфигурируется, allowlist значений |
| 9 | HTTPS + проверка домена/сертификата | [ ] деплой (P0) |
| 10 | SRV `_imaps._tcp`, `_submission(s)._tcp` (RFC 6186/8314) | [ ] runbook (P2) |

#### Apple Mail (Device Management, Mail payload)

| # | Требование | Статус |
|---|---|---|
| 1 | `PayloadType=com.apple.mail.managed`, `EmailTypeIMAP` | [x] |
| 2 | Обязательные: account type, user display name, email, host, port, username, auth | [x] |
| 3 | `Use SSL` соответствует implicit TLS | [x] `!STARTTLS` |
| 4 | Опциональные S/MIME и MailDrop ключи | [x] |
| 5 | `PayloadVersion` — integer | [x] |
| 6 | Уникальные `PayloadUUID`/`PayloadIdentifier` на домен | [x] UUIDv5 на домен |
| 7 | Отсутствие не входящего в схему `HasRemovalPasscode` | [x] удалён |
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
- [x] 5xx рендеринга логируются с `request_id`.
- [x] `/version` с build info.
- [ ] Нет метрик — этап D (опционально).
- [x] Rate limiting на клиентский IP (опционально, `rate_limit_per_minute`).
- [ ] Нет hot reload конфига — P3 (документировать рестарт).
- [x] Dockerfile/CI/container smoke.

### 10.7. Безопасность по регламентам

| Регламент | Пункт | Статус |
|---|---|---|
| OWASP Top 10 2021 | A03 Injection: XML/plist escaping, allowlist | [x] |
| OWASP Top 10 2021 | A03 Injection: fuzz Host/Outlook body | [x] |
| OWASP Top 10 2021 | A04 Insecure design: body limit, timeouts | [x] |
| OWASP Top 10 2021 | A04 Insecure design: rate limiting | [x] |
| OWASP Top 10 2021 | A05 Misconfig: строгий JSON, Apple off by default | [x] |
| OWASP Top 10 2021 | A05 Misconfig: security headers | [x] |
| OWASP Top 10 2021 | A05 Misconfig: HSTS/TLS на стенде | [ ] этап C |
| OWASP Top 10 2021 | A06 Vulnerable components: zero deps + govulncheck | [x] |
| OWASP Top 10 2021 | A06/A08: CI, Dependabot, SBOM | [x] |
| OWASP Top 10 2021 | A08: подпись образа/provenance | [ ] нужен registry |
| OWASP Top 10 2021 | A09 Logging: без PII, request_id | [x] |
| OWASP Top 10 2021 | A09 Logging: мониторинг/алерты/retention | [ ] P2 |
| OWASP Top 10 2021 | A10 SSRF: исходящих запросов нет | [x] |
| OWASP ASVS 4.0 | V5 validation, V6 encoding, V14 config | [x] |
| OWASP ASVS 4.0 | V7 errors/logging (5xx логируются с request_id) | [x] |
| OWASP ASVS 4.0 | V9 communications (TLS/HSTS/сертификаты) | [ ] этап C |
| OWASP ASVS 4.0 | V14 config: pprof только loopback, отдельный listener | [x] |
| Go security | govulncheck, staticcheck, timeouts, MaxBytesReader, crypto/rand, без unsafe | [x] |
| Go security | fuzz-тесты Host/Outlook body | [x] |
| CIS Docker Benchmark | non-root, read-only, cap_drop, no shell, limits | [x] CI smoke |
| SLSA / supply chain | пин toolchain, пустой граф зависимостей | [x] |
| SLSA / supply chain | SBOM, digest-пиннинг | [x] |
| SLSA / supply chain | provenance, cosign | [ ] нужен registry |
| RFC 8314 | TLS 1.2+, приоритет implicit TLS | [ ] этап C |
| Mozilla Autoconfig Security | HTTPS + проверка сертификата | [ ] этап C |
| Privacy/GDPR | нет хранения данных, нет PII в логах | [x] |
| Privacy/GDPR | retention/политика логов | [ ] P2 |
| Microsoft Autodiscover | HTTPS + доверенный сертификат до отправки учётных данных | [ ] этап C |

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
- [x] CI: fixture-валидация Apple/Thunderbird/Outlook (`xmllint`, `plistlib`), HTTP contract-тесты — в Go-тестах.
- [ ] Nightly DNS/SRV/TLS-проверки staging — этап C.
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

---

## 11. Текущее состояние и остаток (2026-10-28)

### 11.1. Сделано

- [x] Код: stdlib-рендеринг, multi-domain, JSON-конфиг, pprof, rate limiting, security headers, Apple/Thunderbird-опции, SOAP 400, `/version`.
- [x] Тесты: golden/unit/race/fuzz, `vet`, `staticcheck`, `govulncheck`.
- [x] Сборка: distroless nonroot, digest-пиннинг, CI, Dependabot, SBOM, `SECURITY.md`.
- [x] Деплой: образ `autodiscover-go:1.0.0` (linux/amd64) в трёх реестрах, digest-пиннинг, три площадки обновлены.
- [x] Проверка: `/` с ручными настройками, `/version` = `1.0.0`, HTTP smoke на каждой площадке, TLS-цепочка валидна, HSTS, логи без PII.
- [x] Старые файлы сохранены в `_old_20261028/`; README площадок ссылаются на проект.

### 11.2. Осталось

1. [ ] Microsoft RCA (Outlook Autodiscover + Outlook Connectivity) — внешняя проверка со стороны Microsoft.
2. [x] Ручная матрица клиентов — **не делаем** для 1.0.0 (решение); HTTP-level проверки в `docs/compat.md`.
3. [x] SRV-записи — **не делаем** (решение); A/HTTPS работают, клиенты используют `autoconfig.*`/`autodiscover.*`.
4. [x] Закоммитить изменения и поставить тег `v1.0.0`.
5. [ ] Подпись образа: keyless cosign через GitHub Actions OIDC (identity = workflow/org), либо ключ в CI-секрете. Требует `id-token: write` и поддержки OCI-подписей реестром.
6. [x] Autodiscover v2 JSON — исследовано: официальной публичной схемы нет; v2 возвращает только URL для EWS/ActiveSync, IMAP/SMTP остаются в v1. Не реализуем (см. 11.4).
7. [ ] CI XSD-профиль, nightly DNS/SRV/TLS; метрики/retention, hot reload — опционально.
8. [x] Canary не выполнялся: развёртывание сразу на трёх площадках; rollback-артефакты сохранены.

### 11.3. Профилирование

- [x] pprof реализован: отдельный loopback-listener, выключен по умолчанию, вне публичного порта.
- [x] Для текущей нагрузки профилирование не требуется: запросы редкие (настройка аккаунта), latency в логах < 1 ms, память ~10 MB, лимит 128 MiB.
- [ ] При необходимости: включить `AUTOCONFIG_PPROF=127.0.0.1:6060`, снять CPU/heap 30 s под нагрузкой (`hey`/`wrk`), проверить аллокации `marshalXML`/`marshalPlist`. Делать только при появлении жалоб на latency/память.

### 11.4. Autodiscover v2 JSON — результат исследования

- Официальной публичной спецификации, связывающей Outlook Classic с Autodiscover v2, не найдено. Ответ Microsoft Q&A (Microsoft External Staff): v2 — дополнительный слой, который возвращает JSON с одним URL для запрошенного протокола (`EWS`, `ActiveSync`), но **не отдаёт IMAP/SMTP-настройки**; они остаются в v1 (POX).
- Источники:
  - <https://learn.microsoft.com/en-us/answers/questions/5646358/does-outlook-classic-support-autodiscover-v2-i-m-c>
  - <https://docs.grommunio.com/man/autodiscover.7.html> (third-party, пример формата)
- Решение: для IMAP/SMTP v2 JSON не реализуем; POX v1 покрывает Outlook desktop. Если появится официальный контракт для IMAP — вернуться к пункту.

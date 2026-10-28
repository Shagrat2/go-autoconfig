# План модернизации go-autoconfig

**Fork:** https://github.com/Shagrat2/go-autoconfig  
**Upstream:** https://github.com/savely-krasovsky/go-autoconfig  
**Базовая версия:** upstream `master` (Go 1.18, Echo 4.11.1)  
**Статус:** план, реализация не начата  
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

- [ ] Собрать upstream-версию: `go build ./...`
- [ ] Запустить с `config.yml`, снять ответы:
  - `POST /autodiscover/autodiscover.xml`
  - `GET /mail/config-v1.1.xml`
  - `GET /email.mobileconfig?email=...`
- [ ] Сохранить ответы как golden-файлы в `testdata/`.
- [ ] Добавить тест, демонстрирующий инъекцию:
  - email `x</LoginName><LoginName>evil` в Outlook;
  - email `x</string><key>evil` в Apple.
- [ ] Зафиксировать текущие версии/размер бинарника.

**Deliverable:** тесты/фикстуры, которые показывают проблему и не дают сломать корректные ответы при рефакторинге.

### Phase 2 — security hardening

- [ ] Убрать `text/template`; использовать `html/template` или `encoding/xml`.
- [ ] Убрать `DisplayName`/`LoginName` с email из Outlook-ответа. Если Outlook потребует `LoginName` — вернуть только после строгой проверки домена и XML-escaping; решение зафиксировать в PR.
- [ ] Убрать `?email=` из Apple-маршрута.
- [ ] Apple-маршрут по умолчанию выключен; при включении — статический профиль с `user@<domain>`.
- [ ] `http.MaxBytesReader` + лимит 1 MiB на тело.
- [ ] Таймауты `http.Server`: `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`.
- [ ] Логи без query/body/email: method, path, status, duration, request_id.
- [ ] Проверить, что email не попадает в логи.
- [ ] Тесты на malicious payload: XML/plist не ломаются, ответ 200/400 по политике.

**Deliverable:** email больше не является входной точкой; нет XML/plist injection; нет unbounded body.

### Phase 3 — переход на stdlib и Go 1.27.3

- [ ] `go mod edit -module=github.com/Shagrat2/go-autoconfig -go=1.27.3`
- [ ] Удалить `echo`, `validator.v9`, `yaml.v2`; `go mod tidy`.
- [ ] `main.go` на `net/http`; `http.ServeMux`; graceful shutdown по SIGTERM.
- [ ] Конфиг JSON:

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

- [ ] Валидация конфига вручную: обязательные поля, порты 1..65535, домены без пробелов.
- [ ] `embed` шаблонов; убрать зависимость от `templates/` рядом с бинарником.
- [ ] Определение домена по `Host`: `autodiscover.<d>`, `autoconfig.<d>`, `<d>`; неизвестный домен → 404.
- [ ] Маршруты:
  - `POST /autodiscover/autodiscover.xml`
  - `GET /autodiscover/autodiscover.xml`
  - `GET /mail/config-v1.1.xml`
  - `GET /.well-known/autoconfig/mail/config-v1.1.xml`
  - `GET /healthz`
  - `GET /email.mobileconfig` — только если `enable_apple=true`
- [ ] `config.example.json`, `README` с примером запуска.
- [ ] `go list -m all` — только собственный модуль.

**Deliverable:** один статический бинарник без сторонних Go-зависимостей.

### Phase 4 — тесты и совместимость

- [ ] Unit-тесты: `domainFromHost`, валидация конфига, неизвестный домен, лимит body.
- [ ] Golden-тесты Outlook/Thunderbird (сравнение XML).
- [ ] Fuzz-тест `Host` и Outlook body: `go test -fuzz=Fuzz -fuzztime=30s`.
- [ ] `go vet ./...`, `staticcheck ./...` (если доступен).
- [ ] `govulncheck ./...` — 0 findings.
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

- [ ] В коде нет приёма `email` из query/body и нет отражения email в XML/plist.
- [ ] Нет `text/template` для пользовательских данных.
- [ ] Тело запроса ограничено; таймауты заданы.
- [ ] `go list -m all` — только `github.com/Shagrat2/go-autoconfig`.
- [ ] `go test ./...` — green.
- [ ] `go vet ./...` — clean.
- [ ] `govulncheck ./...` — 0 findings.
- [ ] Outlook/Thunderbird отвечают корректно; Apple выключен или статический.
- [ ] Два домена работают одним инстансом.
- [ ] Образ non-root, read-only, без shell.
- [ ] Логи не содержат email/query/body.
- [ ] Есть README и пример конфига.

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

# OpenBanking Service

Командный Go-проект: банковские счета и операции, синхронизация с демонстрационным Open Banking API, импорт выписок CSV/XLSX, курсы ЦБ и аналитика.

Реализованы HTTP-сервер, подключение PostgreSQL, миграции, Mock Bank и нормализация его ответов. Основной API пока отвечает `404 Not Found`: бизнес-маршруты добавляются отдельными задачами. Перед запуском HTTP проверяется доступность PostgreSQL.

## Быстрый запуск

Нужен Go версии из [go.mod](go.mod) или совместимой более новой версии.

```sh
# Пароль берётся из локального файла PostgreSQL, см. раздел ниже.
export DATABASE_URL='postgres://postgres@localhost:5432/openBanking?sslmode=disable'
go run ./cmd/api
```

В PowerShell:

```powershell
$env:DATABASE_URL = 'postgres://postgres@localhost:5432/openBanking?sslmode=disable'
go run ./cmd/api
```

Адрес по умолчанию: `http://127.0.0.1:8080`. В другом терминале:

```sh
curl -i http://127.0.0.1:8080/
```

Ответ `404` на этом этапе ожидаем: сервер работает, маршруты ещё не зарегистрированы.

Для остановки нажмите Ctrl+C. Сервис прекращает принимать новые запросы и ждёт завершения активных в пределах `SHUTDOWN_TIMEOUT`. Если срок истёк, соединения принудительно закрываются и процесс завершается с ошибкой.

Чтобы собрать отдельный исполняемый файл:

```sh
# Linux / macOS
go build -o /tmp/openbanking-api ./cmd/api
/tmp/openbanking-api
```

```powershell
# Windows
go build -o "$env:TEMP/openbanking-api.exe" ./cmd/api
& "$env:TEMP/openbanking-api.exe"
```

## Конфигурация

[.env.example](.env.example) перечисляет поддерживаемые настройки без секретов. Приложение читает окружение процесса и самостоятельно не загружает `.env`.

| Переменная                | По умолчанию | Назначение |
|---------------------------|---|---|
| `HTTP_ADDR`               | `127.0.0.1:8080` | Адрес и числовой порт 1–65535 |
| `HTTP_READ_HEADER_TIMEOUT` | `5s` | Время чтения заголовков |
| `HTTP_READ_TIMEOUT`       | `15s` | Время чтения полного запроса |
| `HTTP_WRITE_TIMEOUT`      | `30s` | Таймаут записи ответа |
| `HTTP_IDLE_TIMEOUT`       | `60s` | Ожидание следующего запроса keep-alive |
| `SHUTDOWN_TIMEOUT`        | `10s` | Время на корректную остановку |
| `OG_LEVEL`                 | `info` | Уровень логирования (`debug`, `info`, `warn`, `error`) |

`DATABASE_URL` обязателен; для остальных настроек пустое значение использует значение по умолчанию. Длительности должны быть положительными и иметь единицу измерения, например `500ms`, `5s` или `1m`. Ошибка конфигурации, недоступная БД или занятый порт приводят к сообщению без DSN/пароля и ненулевому коду завершения.

Изменение порта в Linux / macOS:

```sh
HTTP_ADDR=127.0.0.1:9090 go run ./cmd/api
```

В PowerShell:

```powershell
$env:HTTP_ADDR = "127.0.0.1:9090"
go run ./cmd/api
```

Для приёма запросов на всех сетевых интерфейсах задайте `HTTP_ADDR=:8080`. По умолчанию сервер доступен только локально.

Логи запуска и остановки выводятся в stderr в формате JSON.

## PostgreSQL и миграции

Миграции и начальные данные находятся в [migrations](migrations/README.md). Для них нужен PostgreSQL 15+ и `golang-migrate` v4.20.1. Первая миграция создаёт схему, вторая — валюты, категории, Mock Bank и демопользователя. API подключается к БД через `DATABASE_URL`, проверяет доступность и закрывает пул после остановки HTTP.

Для уже заполненной через pgAdmin БД сначала сверяйте структуру: начальные миграции рассчитаны на пустую схему. Не запускайте `up` поверх таблиц, созданных вручную, и не используйте `force`, чтобы пропустить проверку.

### Подключение к установленному PostgreSQL

Если `openBanking` уже создана и миграции применены, достаточно `DATABASE_URL` из быстрого запуска. Пароль хранится в `%APPDATA%\postgresql\pgpass.conf` на Windows или `~/.pgpass` с правами `0600` на Linux/macOS:

```text
localhost:5432:openBanking:postgres:ВАШ_ПАРОЛЬ
```

В пароле символы `:` и `\` экранируются обратным слешем. Пул создаётся в `internal/app`, сначала выполняется `Ping`, затем запускается HTTP. Пул закрывается после завершения HTTP и при ошибке запуска сервера. Миграции API автоматически не выполняет.

| Переменная | По умолчанию | Назначение |
|---|---|---|
| `DATABASE_URL` | обязательно | URL PostgreSQL, схема `postgres://` или `postgresql://` |
| `DB_MAX_CONNS` | `10` | Максимальное число соединений пула, положительный int32 |
| `DB_CONNECT_TIMEOUT` | `5s` | Таймаут установления соединения |
| `DB_STARTUP_TIMEOUT` | `10s` | Общий срок проверки БД при запуске |
| `DB_STATEMENT_TIMEOUT` | `30s` | Серверный таймаут одного SQL-запроса; округляется вверх до миллисекунд |

Ожидание свободного соединения ограничивается контекстом вызывающего сценария. `DB_STATEMENT_TIMEOUT` не ограничивает ожидание пула. При ошибке подключения исходная диагностика драйвера не логируется: она может содержать URL и секреты. Для диагностики используйте `psql`/pgAdmin локально.

### Отдельная БД через Docker Compose

Нужны Docker Engine/Desktop с Compose v2, Go и `golang-migrate` из инструкции миграций. Compose поднимает PostgreSQL 17 на **127.0.0.1:5433**, отдельно от установленного PostgreSQL на 5432.

1. Скопируйте `.env.example` в `.env`, задайте в нём свой `POSTGRES_PASSWORD`. `.env` исключён из Git. Compose читает его автоматически; Go-приложение — нет.
2. Запустите БД: `docker compose up -d --wait postgres`. Healthcheck ждёт готовности сервера.
3. Добавьте в локальный парольный файл строку `localhost:5433:openBanking:postgres:ВАШ_ПАРОЛЬ` с паролем из `.env`.
4. Примените миграции и запустите API из корня репозитория.

PowerShell:

```powershell
$env:MIGRATION_DATABASE_URL = 'pgx5://postgres@localhost:5433/openBanking?sslmode=disable'
migrate -path migrations -database "$env:MIGRATION_DATABASE_URL" up
$env:DATABASE_URL = 'postgres://postgres@localhost:5433/openBanking?sslmode=disable'
go run ./cmd/api
```

Linux/macOS:

```sh
export MIGRATION_DATABASE_URL='pgx5://postgres@localhost:5433/openBanking?sslmode=disable'
migrate -path migrations -database "$MIGRATION_DATABASE_URL" up
export DATABASE_URL='postgres://postgres@localhost:5433/openBanking?sslmode=disable'
go run ./cmd/api
```

Проверка HTTP: `curl -i http://127.0.0.1:8080/` пока возвращает ожидаемый `404`. При старте на недоступной БД HTTP не запускается. `sslmode=disable` в примерах используется для локального окружения.

`docker compose down` останавливает окружение и сохраняет данные в именованном томе. Повторный `up` использует их. `POSTGRES_DB`, `POSTGRES_USER` и пароль инициализируют только новый том; изменение `.env` не меняет пароль существующей БД.

## Проверки

```sh
go fmt ./...
go build ./...
go vet ./...
go test ./...
```

Тесты проверяют конфигурацию, запуск HTTP-сервера, завершение активного запроса при остановке и принудительное закрытие по таймауту. База данных, внешние API и секреты для этих проверок не нужны.

CI проверяет форматирование, сборку, `go vet` и тесты с детектором гонок на PR в `main` и push в `main`. Порядок работы с ветками и ревью описан в [CONTRIBUTING.md](CONTRIBUTING.md).

Интеграционные тесты отделены тегом `integration`. Они не изменяют таблицы, но создают соединения и выполняют короткий `pg_sleep` для проверки таймаута. В PowerShell:

```powershell
$env:POSTGRES_TEST_URL = 'postgres://postgres@localhost:5432/openBanking?sslmode=disable'
go test -tags integration -count=1 -timeout=2m ./tests/integration
```

В Linux/macOS задайте тот же `POSTGRES_TEST_URL` через `export`. Отдельный job CI поднимает окружение из `docker-compose.yml`, проверяет миграции, интеграционные тесты с `-race` и сохранность данных после пересоздания контейнера с прежним томом.

## Структура

| Каталог | Назначение |
|---|---|
| `cmd/api` | Точка входа основного сервиса |
| `cmd/mockbank` | Будущая точка входа демонстрационного банка |
| `internal/app` | Сборка зависимостей и жизненный цикл приложения |
| `internal/config` | Чтение и проверка настроек |
| `internal/domain` | Бизнес-модели и правила |
| `internal/usecase` | Сценарии приложения |
| `internal/infrastructure` | БД, внешние клиенты, парсеры и кеш |
| `internal/delivery/http` | Будущие HTTP-маршруты и обработчики |
| `internal/mockbank` | Будущая реализация демонстрационного банка |
| `api` | Будущие спецификации API |
| `migrations` | SQL-миграции схемы и начальных данных |
| `tests` | Общие интеграционные и сквозные сценарии |

# Как запустить Mock Bank

Все команды выполняются из корня репозитория.

## 1. Задать токен

PowerShell:
```powershell
$env:MOCKBANK_DEMO_TOKEN = "dev-token-123"
```

Linux / macOS:
```bash
export MOCKBANK_DEMO_TOKEN=dev-token-123
```

Токен обязателен, без него сервис не запустится. Переменная действует только в текущей вкладке терминала.

Порт по умолчанию — `127.0.0.1:8081`. Чтобы сменить его, задайте `MOCKBANK_HTTP_ADDR`, например `127.0.0.1:9090`.

## 2. Запустить

```powershell
go run ./cmd/mockbank
```

## 3. Проверить

В другой вкладке терминала:
```powershell
curl.exe http://127.0.0.1:8081/health
curl.exe -H "Authorization: Bearer dev-token-123" http://127.0.0.1:8081/open-banking/v1/mockbank/accounts
```

Первая команда должна вернуть `ok`, вторая — три счёта в JSON.

Остановить сервис: `Ctrl+C` в первой вкладке.
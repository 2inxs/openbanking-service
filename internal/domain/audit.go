package domain

import "time"

// AuditAction определяет тип значимого бизнес- или безопасного события.
type AuditAction string

const (
	AuditActionLogin              AuditAction = "auth.login"
	AuditActionLogout             AuditAction = "auth.logout"
	AuditActionPasswordChanged    AuditAction = "auth.password_changed"
	AuditActionTokenReuseDetected AuditAction = "auth.token_reuse_detected"

	AuditActionAccountCreated  AuditAction = "account.created"
	AuditActionAccountArchived AuditAction = "account.archived"
	AuditActionUserDeleted     AuditAction = "user.deleted"

	AuditActionStatementImported AuditAction = "statement.imported"
	AuditActionStatementExported AuditAction = "statement.exported"

	AuditActionBankConnected    AuditAction = "bank.connected"
	AuditActionBankDisconnected AuditAction = "bank.disconnected"
)

// AuditStatus отражает результат выполнения действия.
type AuditStatus string

const (
	AuditStatusSuccess AuditStatus = "success"
	AuditStatusFailure AuditStatus = "failure"
)

// AuditEvent представляет неизменяемую запись в журнале аудита (append-only).
type AuditEvent struct {
	ID         string
	OccurredAt time.Time // В БД заполняется через now(); для memory заполняется при записи
	UserID     string    // Может быть пустым (например, при неудачной попытке входа)
	Action     AuditAction
	Status     AuditStatus
	IPAddress  string
	UserAgent  string
	RequestID  string
	ResourceID string            // ID счёта, выписки, банковского подключения и т.д.
	Metadata   map[string]string // Только безопасные метаданные (без паролей, токенов, PAN и сумм)
}

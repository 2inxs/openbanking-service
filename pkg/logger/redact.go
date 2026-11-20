package logger

import (
	"regexp"
	"strings"
)

const RedactedValue = "[REDACTED]"

var (
	secretKeyTokens = []string{
		"password",
		"passwd",
		"secret",
		"token",
		"authorization",
		"cookie",
		"cvv",
		"cvc",
		"api_key",
		"apikey",
		"credential",
		"private_key",
	}

	amountExactKeys = map[string]struct{}{
		"amount":      {},
		"amount_kop":  {},
		"balance":     {},
		"balance_kop": {},
		"total":       {},
		"sum":         {},
	}

	cardOrAccountExactKeys = map[string]struct{}{
		"pan":            {},
		"card_number":    {},
		"card":           {},
		"account_number": {},
		"account":        {},
	}

	jwtPattern    = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)
	bearerPattern = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{12,}`)
	panPattern    = regexp.MustCompile(`\b(?:\d[ -]?){13,19}\b`)
)

// SanitizeKeyValue применяет единые правила маскирования для логгера и аудит-лога.
func SanitizeKeyValue(key, val string) string {
	k := strings.ToLower(strings.TrimSpace(key))
	switch {
	case IsSecretKey(k) || IsAmountKey(k) || k == "pan":
		return RedactedValue
	case k == "email" || strings.HasSuffix(k, "_email"):
		return MaskEmail(val)
	case IsCardOrAccountKey(k):
		return MaskLast4(val)
	default:
		return SanitizeStringValue(val)
	}
}

// SanitizeStringValue маскирует токены и похожие на PAN последовательности внутри текста.
func SanitizeStringValue(val string) string {
	if val == "" {
		return val
	}
	res := jwtPattern.ReplaceAllString(val, RedactedValue)
	res = bearerPattern.ReplaceAllString(res, "Bearer "+RedactedValue)
	res = panPattern.ReplaceAllStringFunc(res, func(match string) string {
		digits := strings.NewReplacer(" ", "", "-", "").Replace(match)
		if len(digits) >= 13 && len(digits) <= 19 {
			return MaskLast4(digits)
		}
		return match
	})
	return res
}

// SanitizeStringMap возвращает безопасную копию map[string]string.
func SanitizeStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = SanitizeKeyValue(k, v)
	}
	return dst
}

func IsSecretKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	for _, sub := range secretKeyTokens {
		if strings.Contains(k, sub) {
			return true
		}
	}
	return false
}

func IsAmountKey(key string) bool {
	_, ok := amountExactKeys[strings.ToLower(strings.TrimSpace(key))]
	return ok
}

func IsCardOrAccountKey(key string) bool {
	_, ok := cardOrAccountExactKeys[strings.ToLower(strings.TrimSpace(key))]
	return ok
}

func MaskEmail(email string) string {
	email = strings.TrimSpace(email)
	if email == "" {
		return ""
	}
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return RedactedValue
	}
	runes := []rune(email[:at])
	return string(runes[:1]) + "***" + email[at:]
}

func MaskLast4(val string) string {
	cleaned := strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(val))
	if cleaned == "" {
		return ""
	}
	runes := []rune(cleaned)
	if len(runes) <= 4 {
		return "****"
	}
	return "****" + string(runes[len(runes)-4:])
}

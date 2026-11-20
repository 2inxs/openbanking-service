package dto

type ErrorCode string

const (
	ErrCodeNotFound     ErrorCode = "Resource.NotFound"
	ErrCodeInvalidField ErrorCode = "Field.Invalid"
	ErrCodeInvalidToken ErrorCode = "Token.Invalid"
	ErrCodeInternal     ErrorCode = "Server.Internal"
)

type ErrorResponse struct {
	Code    string      `json:"code"`
	Id      string      `json:"id"`
	Message string      `json:"message"`
	Errors  []ErrorItem `json:"errors"`
}

type ErrorItem struct {
	ErrorCode string `json:"errorCode"`
	Message   string `json:"message"`
	Path      string `json:"path"`
}

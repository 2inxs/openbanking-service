package domain

import "errors"

var (
	ErrAccountNotFound = errors.New("account: not found")
	ErrAccountConflict = errors.New("account: identity or ownership conflict")
)

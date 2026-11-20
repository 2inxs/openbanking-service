package utils

import (
	"fmt"
	"net/url"
	"time"
)

func ParseDateParam(query url.Values, name string) (time.Time, error) {
	raw := query.Get(name)
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be RFC 3339 date-time with time zone, got %q", name, raw)
	}
	return t, nil
}

package persistence

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func enum[T ~string](field, value string, allowed ...T) (T, error) {
	for _, v := range allowed {
		if value == string(v) {
			return v, nil
		}
	}
	var zero T
	return zero, fmt.Errorf("unknown %s value %q", field, value)
}

func jsonColumn[T any](field, raw string) (T, error) {
	var value T
	if raw == "null" {
		return value, fmt.Errorf("decode %s: null is not valid", field)
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("decode %s: %w", field, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return value, fmt.Errorf("decode %s: trailing JSON", field)
	}
	return value, nil
}

func nullableJSON[T any](field string, raw sql.NullString) (*T, error) {
	if !raw.Valid {
		return nil, nil
	}
	value, err := jsonColumn[T](field, raw.String)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

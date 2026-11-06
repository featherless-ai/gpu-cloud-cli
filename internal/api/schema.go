package api

import (
	"encoding/json"

	"featherless.ai/featherless-cloud/internal/errs"
)

// DTO structs are the API schemas: field order is the JSON order, unknown
// response fields are dropped, and the tags say what each field may be.
//   - no omitzero: required; a pointer is required but nullable
//   - omitzero on a pointer or slice: optional, never null
//   - Nullish with omitzero: optional and nullable
//   - format:"uuid" / format:"url": the string's format
//   - default:"<json>": the value used when the API leaves the field out

// Nullish is a field the API may leave out, send as null, or set. Encoding
// keeps whichever it was.
type Nullish[T any] struct {
	Present bool
	// Value is nil when the field is absent or null.
	Value *T
}

// Some is a set Nullish.
func Some[T any](value T) Nullish[T] {
	return Nullish[T]{Present: true, Value: &value}
}

// Null is a Nullish sent as null, e.g. to clear a registry key.
func Null[T any]() Nullish[T] {
	return Nullish[T]{Present: true}
}

func (n Nullish[T]) IsZero() bool {
	return !n.Present
}

func (n Nullish[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(n.Value)
}

func (n *Nullish[T]) UnmarshalJSON(data []byte) error {
	n.Present = true
	n.Value = nil

	if string(data) == "null" {
		return nil
	}

	var value T

	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}

	n.Value = &value

	return nil
}

// RawField is a response field a schema passes through as is.
type RawField struct {
	Name  string
	Value json.RawMessage
}

// parse decodes a response and checks it against T's schema.
func parse[T any](data []byte) (T, error) {
	var zero T
	return zero, errs.New("not implemented")
}

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"featherless.ai/featherless-cloud/internal/errs"
)

// DTO structs are the API's zod schemas: field order is the JSON order,
// unknown response fields are dropped, and the tags say what a field may be.
//   - no omitzero: required; a pointer is required but nullable
//   - omitzero on a pointer or slice: optional, never null
//   - Nullish with omitzero: optional and nullable
//   - format:"uuid" / format:"url": the string's format
//   - default:"<json>": the value used when the API leaves the field out

const unexpectedResponse = "Unexpected response from the Featherless API."

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
	return marshalUnescaped(n.Value)
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

func (n *Nullish[T]) decodeNode(node any) bool {
	n.Present = true
	n.Value = nil

	if node == nil {
		return true
	}

	var value T

	if !decode(reflect.ValueOf(&value).Elem(), node) {
		return false
	}

	n.Value = &value

	return true
}

// RawField is a response field a schema passes through as is.
type RawField struct {
	Name  string
	Value json.RawMessage
}

// nodeDecoder is a type that decodes itself from a parsed JSON node.
type nodeDecoder interface {
	decodeNode(node any) bool
}

var enums = map[reflect.Type][]string{}

// registerEnum declares the values a response enum may take.
func registerEnum[T ~string](values ...T) bool {
	names := make([]string, len(values))

	for i, value := range values {
		names[i] = string(value)
	}

	enums[reflect.TypeFor[T]()] = names

	return true
}

var uuidPattern = regexp.MustCompile(`^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}|00000000-0000-0000-0000-000000000000|ffffffff-ffff-ffff-ffff-ffffffffffff)$`)

// The URL parser deletes ASCII tabs and newlines, and zod keeps its result.
var tabsAndNewlines = strings.NewReplacer("\t", "", "\n", "", "\r", "")

var utf8BOM = []byte("\xef\xbb\xbf")

// parse decodes a response and checks it against T's schema.
func parse[T any](data []byte) (T, error) {
	var out T
	var zero T

	node, err := readJSON(data)

	if err != nil {
		return zero, errs.New(unexpectedResponse)
	}

	if !decode(reflect.ValueOf(&out).Elem(), node) {
		return zero, errs.New(unexpectedResponse)
	}

	return out, nil
}

// object is a JSON object with its members in order. A repeated name keeps
// its first position and its last value, as JSON.parse does.
type object []member

type member struct {
	name  string
	value any
}

func (o object) get(name string) (any, bool) {
	for _, field := range o {
		if field.name == name {
			return field.value, true
		}
	}

	return nil, false
}

// readJSON parses one JSON value into nil, bool, string, json.Number, []any
// and object nodes.
func readJSON(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	node, err := readNode(decoder)

	if err != nil {
		return nil, err
	}

	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("data after the JSON value")
	}

	return node, nil
}

func readNode(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()

	if err != nil {
		return nil, err
	}

	delimiter, isDelimiter := token.(json.Delim)

	if !isDelimiter {
		return token, nil
	}

	if delimiter == '[' {
		items := []any{}

		for decoder.More() {
			item, err := readNode(decoder)

			if err != nil {
				return nil, err
			}

			items = append(items, item)
		}

		_, err := decoder.Token()

		return items, err
	}

	fields := object{}

	for decoder.More() {
		key, err := decoder.Token()

		if err != nil {
			return nil, err
		}

		value, err := readNode(decoder)

		if err != nil {
			return nil, err
		}

		name, _ := key.(string)
		index := slices.IndexFunc(fields, func(field member) bool { return field.name == name })

		if index >= 0 {
			fields[index].value = value
		} else {
			fields = append(fields, member{name: name, value: value})
		}
	}

	_, err = decoder.Token()

	return fields, err
}

// decode sets target from node, reporting whether node fits target's schema.
func decode(target reflect.Value, node any) bool {
	if custom, ok := target.Addr().Interface().(nodeDecoder); ok {
		return custom.decodeNode(node)
	}

	switch target.Kind() {
	case reflect.Pointer:
		if node == nil {
			return false
		}

		value := reflect.New(target.Type().Elem())

		if !decode(value.Elem(), node) {
			return false
		}

		target.Set(value)

		return true
	case reflect.Struct:
		fields, ok := node.(object)

		if !ok {
			return false
		}

		return decodeFields(target, fields)
	case reflect.Slice:
		items, ok := node.([]any)

		if !ok {
			return false
		}

		slice := reflect.MakeSlice(target.Type(), len(items), len(items))

		for i, item := range items {
			if !decode(slice.Index(i), item) {
				return false
			}
		}

		target.Set(slice)

		return true
	case reflect.String:
		text, ok := node.(string)

		if !ok {
			return false
		}

		values, isEnum := enums[target.Type()]

		if isEnum && !slices.Contains(values, text) {
			return false
		}

		target.SetString(text)

		return true
	case reflect.Bool:
		flag, ok := node.(bool)

		if !ok {
			return false
		}

		target.SetBool(flag)

		return true
	case reflect.Int:
		number, ok := node.(json.Number)

		if !ok {
			return false
		}

		value, isFinite := jsNumber(number)
		isSafeInteger := value == math.Trunc(value) && math.Abs(value) <= 1<<53-1

		if !isFinite || !isSafeInteger {
			return false
		}

		target.SetInt(int64(value))

		return true
	case reflect.Float64:
		number, ok := node.(json.Number)

		if !ok {
			return false
		}

		value, isFinite := jsNumber(number)

		if !isFinite {
			return false
		}

		target.SetFloat(value)

		return true
	}

	return false
}

func decodeFields(target reflect.Value, fields object) bool {
	for i := range target.NumField() {
		field := target.Type().Field(i)
		name, options, _ := strings.Cut(field.Tag.Get("json"), ",")

		if !field.IsExported() || name == "" || name == "-" {
			continue
		}

		isOptional := slices.Contains(strings.Split(options, ","), "omitzero")
		value := target.Field(i)
		node, isPresent := fields.get(name)

		if !isPresent {
			fallback, hasDefault := field.Tag.Lookup("default")

			if !hasDefault && isOptional {
				continue
			}

			if !hasDefault {
				return false
			}

			defaultNode, err := readJSON([]byte(fallback))

			if err != nil {
				return false
			}

			node = defaultNode
		}

		isNullablePointer := field.Type.Kind() == reflect.Pointer && !isOptional

		if node == nil && isNullablePointer {
			continue
		}

		if !decode(value, node) {
			return false
		}

		switch field.Tag.Get("format") {
		case "uuid":
			if !uuidPattern.MatchString(value.String()) {
				return false
			}
		case "url":
			trimmed := strings.TrimFunc(value.String(), isJSSpace)

			if !isURL(trimmed) {
				return false
			}

			value.SetString(tabsAndNewlines.Replace(trimmed))
		}
	}

	return true
}

// isURL approximates URL.canParse: an absolute URL, with a host for the
// special schemes.
func isURL(text string) bool {
	cleaned := strings.TrimFunc(tabsAndNewlines.Replace(text), func(r rune) bool { return r <= ' ' })
	parsed, err := url.Parse(cleaned)

	if err != nil || parsed.Scheme == "" {
		return false
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "ws", "wss", "ftp":
		return parsed.Host != ""
	}

	return true
}

// jsNumber reads a JSON number the way JSON.parse does; false for one too
// large to be finite.
func jsNumber(number json.Number) (float64, bool) {
	value, err := strconv.ParseFloat(string(number), 64)

	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0, false
	}

	if math.IsInf(value, 0) {
		return 0, false
	}

	// JSON.stringify writes -0 as 0.
	if value == 0 {
		value = 0
	}

	return value, true
}

// isJSSpace is what String.prototype.trim removes.
func isJSSpace(r rune) bool {
	return r == '\uFEFF' || (unicode.IsSpace(r) && r != '\u0085')
}

// marshalUnescaped encodes like JSON.stringify, without HTML escaping, so
// the output encoder's own escaping setting decides.
func marshalUnescaped(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(value); err != nil {
		return nil, err
	}

	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

// writeNode encodes a parsed node as JSON.stringify would re-encode it.
func writeNode(buffer *bytes.Buffer, node any) {
	switch value := node.(type) {
	case object:
		buffer.WriteByte('{')

		for i, field := range value {
			if i > 0 {
				buffer.WriteByte(',')
			}

			name, _ := marshalUnescaped(field.name)
			buffer.Write(name)
			buffer.WriteByte(':')
			writeNode(buffer, field.value)
		}

		buffer.WriteByte('}')
	case []any:
		buffer.WriteByte('[')

		for i, item := range value {
			if i > 0 {
				buffer.WriteByte(',')
			}

			writeNode(buffer, item)
		}

		buffer.WriteByte(']')
	case json.Number:
		number, isFinite := jsNumber(value)

		if !isFinite {
			buffer.WriteString("null")
			return
		}

		encoded, _ := json.Marshal(number)
		buffer.Write(encoded)
	case string:
		encoded, _ := marshalUnescaped(value)
		buffer.Write(encoded)
	case bool:
		buffer.WriteString(strconv.FormatBool(value))
	default:
		buffer.WriteString("null")
	}
}

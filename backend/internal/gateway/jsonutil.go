package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
)

func decodeJSONObject(encoded []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, ErrInvalidInput
	}
	if value == nil {
		return nil, ErrInvalidInput
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, ErrInvalidInput
	}
	return value, nil
}

func marshalJSONObject(value map[string]any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

func marshalJSONObjectWithin(value map[string]any, maximum int64) ([]byte, error) {
	encoded, err := marshalJSONObject(value)
	if err != nil {
		return nil, err
	}
	if int64(len(encoded)) > maximum {
		return nil, ErrResponseTooBig
	}
	return encoded, nil
}

func nestedObject(value map[string]any, key string) map[string]any {
	result, _ := value[key].(map[string]any)
	return result
}

func intField(value map[string]any, key string) (int64, bool) {
	raw, exists := value[key]
	if !exists {
		return 0, false
	}
	switch typed := raw.(type) {
	case json.Number:
		parsed, err := strconv.ParseInt(string(typed), 10, 64)
		return parsed, err == nil && parsed >= 0
	case float64:
		parsed := int64(typed)
		return parsed, float64(parsed) == typed && parsed >= 0
	case int64:
		return typed, typed >= 0
	default:
		return 0, false
	}
}

func intFieldDefault(value map[string]any, key string, fallback int64) (int64, bool) {
	if _, exists := value[key]; !exists {
		return fallback, true
	}
	return intField(value, key)
}

func intPointer(value int64) *int64 { return &value }

func stringField(value map[string]any, key string) string {
	result, _ := value[key].(string)
	return result
}

func coalesce(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

package app

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

type JSONValue struct {
	Data any
}

func (j *JSONValue) Scan(value any) error {
	if value == nil {
		j.Data = nil
		return nil
	}
	var data []byte
	switch typed := value.(type) {
	case []byte:
		data = typed
	case string:
		data = []byte(typed)
	default:
		return fmt.Errorf("scanning json value from %T", value)
	}
	if len(data) == 0 {
		j.Data = nil
		return nil
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("decoding json value: %w", err)
	}
	j.Data = decoded
	return nil
}

func (j JSONValue) Value() (driver.Value, error) {
	if j.Data == nil {
		return "null", nil
	}
	data, err := json.Marshal(j.Data)
	if err != nil {
		return nil, fmt.Errorf("encoding json value: %w", err)
	}
	return string(data), nil
}

func (j JSONValue) MarshalJSON() ([]byte, error) {
	if j.Data == nil {
		return []byte("null"), nil
	}
	return json.Marshal(j.Data)
}

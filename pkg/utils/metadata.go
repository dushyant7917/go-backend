package utils

import (
	"database/sql/driver"
	"encoding/json"
)

// Metadata is a custom type for JSONB fields
type Metadata map[string]interface{}

// Scan implements the sql.Scanner interface for Metadata
func (m *Metadata) Scan(value interface{}) error {
	if value == nil {
		*m = make(Metadata)
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(bytes, m)
}

// Value implements the driver.Valuer interface for Metadata
func (m Metadata) Value() (driver.Value, error) {
	if m == nil {
		return json.Marshal(make(map[string]interface{}))
	}
	return json.Marshal(m)
}

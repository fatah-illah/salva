package domain

import (
	"database/sql/driver"
	"encoding/json"
	"time"
)

// Message represents a message in the system
type Message struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Payload   JSONB     `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}

// JSONB is a type for handling JSONB fields in PostgreSQL
type JSONB map[string]any

// Scan implements the sql.Scanner interface
func (j *JSONB) Scan(value interface{}) error {
	if value == nil {
		*j = nil
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return json.Unmarshal(value.([]byte), j)
	}
	return json.Unmarshal(b, j)
}

// Value implements the driver.Valuer interface
func (j JSONB) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return json.Marshal(j)
}

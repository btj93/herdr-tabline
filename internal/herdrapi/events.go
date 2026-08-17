package herdrapi

import "encoding/json"

type Event struct {
	Kind string          `json:"event"`
	Data json.RawMessage `json:"data"`
}

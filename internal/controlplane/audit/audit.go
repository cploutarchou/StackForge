package audit

import "time"

type Log struct {
	ID           string    `json:"id"`
	Actor        string    `json:"actor"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	BeforeJSON   string    `json:"before_json"`
	AfterJSON    string    `json:"after_json"`
	Error        string    `json:"error"`
	CreatedAt    time.Time `json:"created_at"`
}

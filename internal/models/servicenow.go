package models

// ServiceNowIncident represents the payload structure for creating/updating
// incidents in ServiceNow via the Table API.
type ServiceNowIncident struct {
	ShortDescription string `json:"short_description"`
	Description      string `json:"description"`
	Impact           string `json:"impact"`
	Urgency          string `json:"urgency"`
	Category         string `json:"category"`
	Subcategory      string `json:"subcategory"`
	AssignmentGroup  string `json:"assignment_group,omitempty"`
	CallerID         string `json:"caller_id,omitempty"`
	CorrelationID    string `json:"correlation_id"`
}

// ServiceNowResponse represents the response from ServiceNow Table API.
type ServiceNowResponse struct {
	Result ServiceNowResult `json:"result"`
}

// ServiceNowListResponse represents the response from ServiceNow Table API for list queries.
type ServiceNowListResponse struct {
	Result []ServiceNowResult `json:"result"`
}

// ServiceNowResult represents a single incident record from ServiceNow.
type ServiceNowResult struct {
	SysID            string `json:"sys_id"`
	Number           string `json:"number"`
	State            string `json:"state"`
	CorrelationID    string `json:"correlation_id"`
	ShortDescription string `json:"short_description"`
}

// ServiceNowUpdatePayload represents the payload for updating an incident state.
type ServiceNowUpdatePayload struct {
	State        string `json:"state"`
	CloseCode    string `json:"close_code,omitempty"`
	CloseNotes   string `json:"close_notes,omitempty"`
	RootCause    string `json:"u_root_cause,omitempty"`
	RestoredDate string `json:"u_restored_date,omitempty"`
}

// ServiceNow incident state constants.
const (
	// StateResolved indicates the incident is resolved (state 6 in ServiceNow).
	StateResolved = "6"
)

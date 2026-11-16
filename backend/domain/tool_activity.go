package domain

// ToolActivity records an inner Code Mode operation with a bounded output.
// It stays outside model context until printed by the script or explicitly
// retrieved by its owner through tool_output.
type ToolActivity struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   string `json:"content"`

	Network       bool   `json:"network"`
	Justification string `json:"justification"`
}

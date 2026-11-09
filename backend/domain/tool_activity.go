package domain

// ToolActivity is UI-only evidence for an inner Code Mode operation. Its output
// never enters the model context unless the script explicitly prints it.
type ToolActivity struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   string `json:"content"`

	Network       bool   `json:"network"`
	Justification string `json:"justification"`
}

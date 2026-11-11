package providers

import (
	"strings"
	"unicode"
)

// ResponseError retains the terminal provider diagnostic without retaining the
// response envelope, unfinished text, encrypted reasoning or tool arguments.
type ResponseError struct {
	Code    string
	Message string
	Param   string
	Reason  string
}

func (failure *ResponseError) Error() string {
	details := []string{}

	if failure.Code != "" {
		details = append(details, diagnosticField(failure.Code, 128))
	}

	if failure.Param != "" {
		details = append(details, "parameter: "+diagnosticField(failure.Param, 256))
	}

	if failure.Reason != "" {
		details = append(details, "reason: "+diagnosticField(failure.Reason, 128))
	}

	text := "provider did not complete the response"

	if len(details) > 0 {
		text += " (" + strings.Join(details, "; ") + ")"
	}

	if failure.Message != "" {
		text += ": " + diagnosticField(failure.Message, 2000)
	}

	if len(details) == 0 && failure.Message == "" {
		text += "; retry the request"
	}

	return text
}

func responseFailure(envelope map[string]any) *ResponseError {
	response := envelope

	if value, ok := envelope["response"].(map[string]any); ok {
		response = value
	}

	detail := response

	if value, ok := response["error"].(map[string]any); ok {
		detail = value
	}

	code := asString(detail["code"])
	if code == "" {
		code = asString(detail["type"])
	}

	return &ResponseError{
		Code:    code,
		Message: asString(detail["message"]),
		Param:   asString(detail["param"]),
		Reason:  asString(asObject(response["incomplete_details"])["reason"]),
	}
}

func diagnosticField(value any, limit int) string {
	text := strings.TrimSpace(strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return ' '
		}

		return character
	}, asString(value)))

	characters := []rune(text)
	if len(characters) > limit {
		return string(characters[:limit]) + "…"
	}

	return text
}

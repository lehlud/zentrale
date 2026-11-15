package domain

import "testing"

func TestQuestionRequiresAnswer(t *testing.T) {
	for _, test := range []struct {
		mode   QuestionMode
		status string
		want   bool
	}{
		{QuestionAdvisory, "open", false},
		{QuestionBlocking, "open", true},
		{"", "open", true},
		{"unknown", "open", true},
		{QuestionBlocking, "answered", false},
		{QuestionBlocking, "dismissed", false},
		{QuestionBlocking, "withdrawn", false},
	} {
		question := Question{Mode: test.mode, Status: test.status}
		if got := question.RequiresAnswer(); got != test.want {
			t.Errorf("mode %q, status %q: RequiresAnswer = %v, want %v", test.mode, test.status, got, test.want)
		}
	}
}

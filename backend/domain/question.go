package domain

// QuestionMode describes whether an answer is needed before any safe work can
// continue. It never grants authorization or replaces environmental approval.
type QuestionMode string

const (
	QuestionAdvisory QuestionMode = "advisory"
	QuestionBlocking QuestionMode = "blocking"
)

// RequiresAnswer keeps saved questions from before modes were introduced
// conservative. Unknown persisted modes also fail closed; only an explicitly
// advisory question permits progress without an answer.
func (question Question) RequiresAnswer() bool {
	return question.Status == "open" && question.Mode != QuestionAdvisory
}

package agent

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"
)

// Reject cheap, purely structural failures before paying for model review.
// Remote helpers still validate paths, current contents and the complete patch;
// passing this check never grants permission or proves a mutation will succeed.
func validateToolArguments(request interaction) error {
	switch request.Action {
	case "note_save":
		title := stringArg(request.Arguments, "title")
		if title == "" || utf8.RuneCountInString(title) > 30 || utf8.RuneCountInString(stringArg(request.Arguments, "content")) > 350 {
			return errors.New("note limit: title 1–30, content 350 characters")
		}
	case "read_file":
		_, err := fileReadOptions(request.Arguments)
		return err
	case "apply_patch":
		patch := stringArg(request.Arguments, "input")
		if len(patch) > 1024*1024 || !noSecrets(patch) {
			return errors.New("patch exceeds 1 MiB or contains invalid characters")
		}

		lines := strings.Split(strings.TrimSpace(patch), "\n")
		if len(lines) < 3 || strings.TrimSuffix(lines[0], "\r") != "*** Begin Patch" ||
			strings.TrimSuffix(lines[len(lines)-1], "\r") != "*** End Patch" {
			return errors.New("patch must start with *** Begin Patch and end with *** End Patch")
		}

		paths := map[string]bool{}
		operations := 0
		for _, line := range lines[1 : len(lines)-1] {
			line = strings.TrimSuffix(line, "\r")
			for _, marker := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "} {
				if !strings.HasPrefix(line, marker) {
					continue
				}

				filename := strings.TrimPrefix(line, marker)
				if filename == "" {
					return errors.New("patch needs a nonempty file path")
				}

				cleaned := path.Clean(filename)
				if paths[cleaned] {
					return fmt.Errorf("patch repeats path %q; use one file marker with multiple @@ chunks for each path", filename)
				}

				paths[cleaned] = true
				if marker != "*** Move to: " {
					operations++
				}
			}
		}

		if operations == 0 || operations > 50 {
			return errors.New("patch must change between 1 and 50 files")
		}
	}

	return nil
}

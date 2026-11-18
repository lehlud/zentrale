package domain

import "testing"

func TestRepositorySSHURLRequiresAnSSHTransport(t *testing.T) {
	for _, address := range []string{
		"git@code.example.org:team/project.git",
		"ssh://git@code.example.org:2222/team/project.git",
		"ssh://code.example.org/team/project.git",
		"ssh://git@[::1]:2222/team/project%2Egit",
	} {
		if err := ValidateRepositorySSHURL(address); err != nil {
			t.Errorf("rejected %q: %v", address, err)
		}
	}
	for _, address := range []string{
		"", "https://code.example.org/team/project.git", "file:///repo.git", "ext::command",
		"https://git@code.example.org:/repo.git", "git@-option:repo.git",
		"ssh://git:password@code.example.org/team/project.git",
		"ssh://git@code.example.org:65536/team/project.git",
		"ssh://git@code.example.org/team/project%0A.git",
		"ssh://git@code.example.org/team/project.git?query=1",
		"ssh://git@code.example.org/", "git@code.example.org:",
	} {
		if err := ValidateRepositorySSHURL(address); err == nil {
			t.Errorf("accepted %q", address)
		}
	}
}

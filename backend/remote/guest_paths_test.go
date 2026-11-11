package remote

import "testing"

func TestStableGuestWorkspaceMapsOnlyInsideProject(t *testing.T) {
	for _, test := range []struct{ requested, expected string }{
		{ManagedGuestWorkspace, "/srv/project"},
		{ManagedGuestWorkspace + "/backend/agent/tools.go", "/srv/project/backend/agent/tools.go"},
		{ManagedGuestWorkspace + "/backend/../README.md", "/srv/project/README.md"},
		{"frontend/src/app.tsx", "/srv/project/frontend/src/app.tsx"},
		{"/srv/project/README.md", "/srv/project/README.md"},
	} {
		got, err := Workspace("/srv/project", test.requested)
		if err != nil || got != test.expected {
			t.Fatal(test.requested, got, err)
		}
	}
	for _, requested := range []string{
		ManagedGuestWorkspace + "/../secrets", ManagedGuestWorkspace + "/../../etc/passwd",
		"/home/zentrale/project-other/file", "/etc/passwd", ManagedGuestWorkspace + "/file\x00",
	} {
		if path, err := Workspace("/srv/project", requested); err == nil {
			t.Fatal("guest alias escaped project", requested, path)
		}
	}
}

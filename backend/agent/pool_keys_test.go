package agent

import (
	"strings"
	"testing"
)

const managedPoolYAML = `id: managed
name: Managed pool
max_machines: 1
shared_home: true
single_user: true
sandbox: auto
machines:
  - id: first
    name: First
    host: first.example.org
    user: developer
  - id: second
    name: Second
    host: second.example.org
    user: developer
`

func TestPoolImportManagesStableCredentialsAndRejectsKeySelection(t *testing.T) {
	engine, _ := testEngine(t, nil)
	before, _ := engine.Store.ListPrivSSHKeys()
	pool, err := engine.SavePool(managedPoolYAML)
	if err != nil || pool.KeyID <= 0 {
		t.Fatal(pool, err)
	}

	for _, member := range pool.Machines {
		machine, err := engine.Remote.Connector.Machine(pool.ID + "/" + member.ID)
		if err != nil || machine.KeyID != pool.KeyID {
			t.Fatal("SSH connector lost the managed pool key", machine, err)
		}
	}

	updated, err := engine.SavePool(strings.Replace(managedPoolYAML, "name: Managed pool", "name: Renamed pool", 1))
	if err != nil || updated.KeyID != pool.KeyID {
		t.Fatal("reimport rotated the managed key", updated, err)
	}

	for _, yaml := range []string{
		managedPoolYAML + "key_id: 1\n",
		managedPoolYAML + "keyId: 1\n",
		strings.Replace(managedPoolYAML, "    user: developer", "    key_id: 0\n    user: developer", 1),
		strings.Replace(managedPoolYAML, "    user: developer", "    key_id: null\n    user: developer", 1),
		strings.Replace(managedPoolYAML, "    user: developer", "    <<: {key_id: 1}\n    user: developer", 1),
		managedPoolYAML + "unexpected: value\n",
		managedPoolYAML + "known_hosts: invalid\n",
		strings.Replace(managedPoolYAML, "    user: developer", "    jumps: [managed/first]\n    user: developer", 1),
	} {
		if _, err := engine.SavePool(yaml); err == nil {
			t.Fatal("invalid pool or SSH key selection was accepted", yaml)
		}
	}

	after, _ := engine.Store.ListPrivSSHKeys()
	if len(after) != len(before)+1 {
		t.Fatal("reimport or failed validation allocated orphan keys")
	}
}

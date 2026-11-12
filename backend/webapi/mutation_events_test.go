package webapi

import "testing"

func TestHTTPKeyMutationPushesUpdatedSnapshotWithoutClientRefetch(t *testing.T) {
	fixture := newAPIFixture(t)
	requireData(t, fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.org"})}`, nil))
	conn := openReadOnlyPushProtocolV1(t, fixture)
	if err := conn.WriteJSON(socketQueryRequest{Type: "subscribe", RequestID: "keys", Query: `query{sshKeys{id}}`}); err != nil {
		t.Fatal(err)
	}
	before := requireData(t, readReadOnlyPushProtocolV1(t, conn, "keys"))["sshKeys"].([]any)
	requireData(t, fixture.query(t, `mutation{generateSSHKey{id}}`, nil))
	// No client frame or manual Engine.Emit after the HTTP mutation.
	after := requireData(t, readReadOnlyPushProtocolV1(t, conn, "keys"))["sshKeys"].([]any)
	if len(after) != len(before)+1 {
		t.Fatal("key mutation did not trigger a server push", before, after)
	}
}

func TestOnlySelectedMutationPublishesSubscriptionInvalidation(t *testing.T) {
	for _, fixture := range []struct {
		query, name string
		want        bool
	}{
		{`query{status{authenticated}}`, "", false},
		{`mutation{logout}`, "", true},
		{`query Read{status{authenticated}} mutation Change{logout}`, "Read", false},
		{`query Read{status{authenticated}} mutation Change{logout}`, "Change", true},
		{`query Read{status{authenticated}} mutation Change{logout}`, "Missing", false},
		{`query{status{authenticated}} # mutation{logout}`, "", false},
		{`not graphql`, "", false},
	} {
		if got := isMutationOperation(fixture.query, fixture.name); got != fixture.want {
			t.Errorf("%q %q: %v", fixture.query, fixture.name, got)
		}
	}
}

package remote

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/internal/testssh"
)

func TestPreviewPortUsesAuthenticatedGuestRelayWithoutHostFallback(t *testing.T) {
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	peer, err := testssh.StartEditorPeer(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	key, err := store.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	public, _ := DecodePublicKey(peer.PublicKey)
	if err := store.TrustSSHKey(public); err != nil {
		t.Fatal(err)
	}
	machine := domain.Machine{ID: "guest-test", Host: peer.Host, Port: peer.Port, User: "test", KeyID: key.ID}
	if err := store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}
	connection, err := (&Connector{Store: store}).Dial(context.Background(), machine.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	lines := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		client, err := listener.Accept()
		if err != nil {
			return
		}
		defer client.Close()
		reader := bufio.NewReader(client)
		line, _ := reader.ReadString('\n')
		lines <- line
		_, _ = io.WriteString(client, "OK\n")
		_, _ = io.Copy(client, reader)
	}()
	session := &editorSession{connection: connection, runtime: editorRuntime{address: listener.Addr().String(), token: "guest-secret"}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := dialEditorPort(ctx, session, 1235)
	if err != nil {
		t.Fatal(err)
	}
	if line := <-lines; line != "guest-secret 1235\n" {
		t.Fatal("wrong guest destination", line)
	}
	_, _ = io.WriteString(stream, "guest-api")
	data := make([]byte, len("guest-api"))
	if _, err := io.ReadFull(stream, data); err != nil || string(data) != "guest-api" {
		t.Fatal(string(data), err)
	}
	stream.Close()
	<-done
	listener.Close()
	if stream, err := dialEditorPort(ctx, session, 1235); err == nil {
		stream.Close()
		t.Fatal("unavailable guest relay fell back to a host port")
	}
	editors := NewEditors(NewManager(&Connector{Store: store}))
	defer editors.Close()
	for _, port := range []int{0, -1, 65536, 9842} {
		if _, _, err := editors.PortTransport(domain.Project{}, "", port); err == nil || !strings.Contains(err.Error(), "invalid preview port") {
			t.Fatal("invalid preview port was accepted", port, err)
		}
	}
}

package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/internal/testssh"
	"golang.org/x/crypto/ssh"
)

func TestTerminalSSHProtocolAndCancellation(t *testing.T) {
	frames := make(chan map[string]any, 4)
	closed := make(chan struct{})
	peer, err := testssh.StartWithCommandHandler(func(command string, stdin io.Reader, stdout, stderr io.Writer) int {
		defer close(closed)
		if !strings.Contains(command, "--protocol") || strings.Contains(command, "vscode") {
			t.Error("wrong terminal command")
		}
		decoder := json.NewDecoder(stdin)
		var frame map[string]any
		if err := decoder.Decode(&frame); err != nil {
			return 1
		}
		frames <- frame
		io.WriteString(stdout, "\x00ZENTRALE_TERMINAL_READY\n")
		stdout.Write([]byte{0, 255, 'x'})
		for {
			var frame map[string]any
			if decoder.Decode(&frame) != nil {
				return 0
			}
			frames <- frame
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	client, err := ssh.Dial("tcp", net.JoinHostPort(peer.Host, strconv.Itoa(peer.Port)), &ssh.ClientConfig{User: "test", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	terminal, err := StartTerminal(ctx, &Connection{Client: client}, domain.Machine{Sandbox: SandboxDisabled}, domain.Project{ID: "project", Directory: "/tmp"}, EditorTarget{AgentID: "owner", TemporaryDirectory: "/tmp/terminal"}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	initial := <-frames
	if initial["cols"] != float64(80) || initial["rows"] != float64(24) {
		t.Fatal(initial)
	}
	output := make([]byte, 3)
	if _, err := io.ReadFull(terminal, output); err != nil || string(output) != string([]byte{0, 255, 'x'}) {
		t.Fatal(output, err)
	}
	if err := terminal.Input([]byte{3, 255}); err != nil {
		t.Fatal(err)
	}
	input := <-frames
	if input["data"] != base64.StdEncoding.EncodeToString([]byte{3, 255}) {
		t.Fatal(input)
	}
	if err := terminal.Resize(132, 43); err != nil {
		t.Fatal(err)
	}
	resize := <-frames
	if fmt.Sprint(resize["type"]) != "resize" || resize["cols"] != float64(132) {
		t.Fatal(resize)
	}
	if terminal.Resize(0, 24) == nil || terminal.Input(make([]byte, 65537)) == nil {
		t.Fatal("bounds not enforced")
	}
	cancel()
	terminal.Close()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("SSH stdin/session leaked")
	}
}

package testssh

import (
	"crypto/ed25519"
	"errors"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestCommandHandlerExitStatusReachesSSHClient(t *testing.T) {
	server, err := StartWithCommandHandler(func(command string, _ io.Reader, stdout, _ io.Writer) int {
		_, _ = io.WriteString(stdout, command)
		return 37
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	_, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	public, _, _, _, err := ssh.ParseAuthorizedKey([]byte(server.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	client, err := ssh.Dial("tcp", net.JoinHostPort(server.Host, strconv.Itoa(server.Port)), &ssh.ClientConfig{
		User: "test", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.FixedHostKey(public), Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	output, err := session.CombinedOutput("status fixture")
	var exit *ssh.ExitError
	if string(output) != "status fixture" || !errors.As(err, &exit) || exit.ExitStatus() != 37 {
		t.Fatalf("%q %v", output, err)
	}
}

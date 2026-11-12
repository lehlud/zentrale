package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// The peer never executes a shell or contacts a configured machine. Stalls
// affect only protocol acknowledgements on this loopback test connection.
func commandLifecyclePeer(t *testing.T, stall string, run func(string, ssh.Channel, <-chan *ssh.Request)) (*ssh.Client, <-chan struct{}) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	configuration := &ssh.ServerConfig{NoClientAuth: true}
	configuration.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		socket, err := listener.Accept()
		if err != nil {
			return
		}
		server, channels, requests, err := ssh.NewServerConn(socket, configuration)
		if err != nil {
			_ = socket.Close()
			return
		}
		defer server.Close()
		disconnected := make(chan struct{})
		go func() { _ = server.Wait(); close(disconnected) }()
		go ssh.DiscardRequests(requests)
		var workers sync.WaitGroup
		for incoming := range channels {
			workers.Add(1)
			go func() {
				defer workers.Done()
				if stall == "open" {
					<-disconnected
					return
				}
				if stall == "slow-open" {
					time.Sleep(100 * time.Millisecond)
				}
				channel, requests, err := incoming.Accept()
				if err != nil {
					return
				}
				defer channel.Close()
				for request := range requests {
					if request.Type != "exec" {
						_ = request.Reply(false, nil)
						continue
					}
					if stall == "exec" {
						<-disconnected
						return
					}
					if stall == "slow-exec" {
						time.Sleep(100 * time.Millisecond)
					}
					var payload struct{ Command string }
					_ = ssh.Unmarshal(request.Payload, &payload)
					_ = request.Reply(true, nil)
					run(payload.Command, channel, requests)
					return
				}
			}()
		}
		workers.Wait()
	}()
	client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{
		User: "fixture", HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 2 * time.Second,
	})
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("SSH fixture did not shut down")
		}
	})
	return client, done
}

func fixtureCommandExit(channel ssh.Channel, status uint32) {
	_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
}

func TestCommandAlreadyCancelledDoesNotOpenSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A nil client makes any accidental channel-open attempt fail immediately.
	if _, err := Run(ctx, nil, "must-not-start"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCommandExitDoesNotWaitForNoninteractiveInputEOF(t *testing.T) {
	client, _ := commandLifecyclePeer(t, "", func(_ string, channel ssh.Channel, _ <-chan *ssh.Request) {
		_, _ = io.WriteString(channel, "finished before stdin EOF")
		fixtureCommandExit(channel, 0)
	})
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	done := make(chan error, 1)
	go func() {
		text, err := RunPipe(context.Background(), client, "fixture", input)
		if !strings.Contains(text, "finished before stdin EOF") && err == nil {
			err = errors.New("lost command output")
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("exited command remained running waiting for unrelated stdin EOF")
	}
}

func TestCommandSetupDeadlineReleasesUnresponsiveTransport(t *testing.T) {
	for _, stall := range []string{"open", "exec"} {
		t.Run(stall, func(t *testing.T) {
			client, peerDone := commandLifecyclePeer(t, stall, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := Run(ctx, client, "fixture"); done <- err }()
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("setup ignored command deadline")
			}
			if stall == "open" {
				select {
				case <-peerDone:
				case <-time.After(3 * time.Second):
					t.Fatal("setup cancellation left the unresponsive transport open")
				}
			} else {
				// This peer withholds exec's reply but its SSH mux still handles
				// channel closure. Closing the invocation releases Start; that
				// is not evidence that the shared transport should be killed.
				time.Sleep(1200 * time.Millisecond)
				ping := make(chan error, 1)
				go func() { _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); ping <- err }()
				select {
				case err := <-ping:
					if err != nil {
						t.Fatal("exec cancellation closed a responsive transport", err)
					}
				case <-time.After(time.Second):
					t.Fatal("responsive transport did not answer after exec cancellation")
				}
			}
		})
	}
}

func TestCommandSetupCancellationWithLateAckKeepsNeighborAlive(t *testing.T) {
	for _, delay := range []string{"slow-open", "slow-exec"} {
		t.Run(delay, func(t *testing.T) {
			release := make(chan struct{})
			neighborStarted := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			client, _ := commandLifecyclePeer(t, delay, func(command string, channel ssh.Channel, _ <-chan *ssh.Request) {
				if strings.Contains(command, "neighbor") {
					close(neighborStarted)
					<-release
				}
				_, _ = io.WriteString(channel, "survived late ack")
				fixtureCommandExit(channel, 0)
			})
			neighborDone := make(chan error, 1)
			go func() { _, err := Run(context.Background(), client, "neighbor"); neighborDone <- err }()
			select {
			case <-neighborStarted:
			case <-time.After(time.Second):
				t.Fatal("neighbor did not start")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			cancelled := make(chan error, 1)
			go func() { _, err := Run(ctx, client, "late-ack"); cancelled <- err }()
			select {
			case err := <-cancelled:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("late setup ack delayed local cancellation")
			}
			// Exercise late-result cleanup, not merely the initial return.
			time.Sleep(200 * time.Millisecond)
			select {
			case err := <-neighborDone:
				t.Fatal("ordinary setup cancellation killed neighbor", err)
			default:
			}
			once.Do(func() { close(release) })
			select {
			case err := <-neighborDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("neighbor could not complete after late setup ack")
			}
		})
	}
}

func TestCommandDeadlineAfterOutputEOFKeepsNeighborAlive(t *testing.T) {
	release := make(chan struct{})
	neighborStarted := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	client, _ := commandLifecyclePeer(t, "", func(command string, channel ssh.Channel, requests <-chan *ssh.Request) {
		if strings.Contains(command, "neighbor") {
			close(neighborStarted)
			<-release
			_, _ = io.WriteString(channel, "neighbor completed")
			fixtureCommandExit(channel, 0)
			return
		}
		_, _ = io.WriteString(channel, "last output before EOF")
		_ = channel.CloseWrite() // EOF is not an exit-status receipt.
		for range requests {
		}
	})
	neighborDone := make(chan error, 1)
	go func() {
		text, err := Run(context.Background(), client, "neighbor")
		if err == nil && !strings.Contains(text, "neighbor completed") {
			err = errors.New("neighbor output lost")
		}
		neighborDone <- err
	}()
	select {
	case <-neighborStarted:
	case <-time.After(time.Second):
		t.Fatal("neighbor did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	targetDone := make(chan error, 1)
	go func() {
		text, err := Run(ctx, client, "missing-exit-status")
		if !strings.Contains(text, "last output before EOF") {
			err = errors.New("deadline lost captured output")
		}
		targetDone <- err
	}()
	select {
	case err := <-targetDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("output EOF/Wait stalled beyond command deadline")
	}
	select {
	case err := <-neighborDone:
		t.Fatal("normal command timeout interrupted neighbor", err)
	default:
	}
	once.Do(func() { close(release) })
	select {
	case err := <-neighborDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("healthy neighbor could not complete")
	}
}

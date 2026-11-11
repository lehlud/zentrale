package remote

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"golang.org/x/crypto/ssh"
)

type HostKeyError struct{ Host, Fingerprint, PublicKey string }

func (e *HostKeyError) Error() string {
	return fmt.Sprintf("untrusted SSH host %s: %s; public key: %s", e.Host, e.Fingerprint, e.PublicKey)
}

type Connection struct {
	Client *ssh.Client
	chain  []*ssh.Client
	shared *sharedConnection
	closed sync.Once
}

type sharedConnection struct {
	mutex      sync.Mutex
	users      int
	chain      []*ssh.Client
	identities []verifiedHostIdentity
}

type verifiedHostIdentity struct {
	machineID string
	address   string
	remote    net.Addr
	key       ssh.PublicKey
}

func (connection *Connection) Close() {
	connection.closed.Do(func() {
		chain := connection.chain

		if connection.shared != nil {
			connection.shared.mutex.Lock()
			connection.shared.users--

			if connection.shared.users != 0 {
				connection.shared.mutex.Unlock()
				return
			}

			chain = connection.shared.chain
			connection.shared.mutex.Unlock()
		}

		for index := len(chain) - 1; index >= 0; index-- {
			_ = chain[index].Close()
		}
	})
}

// Retain gives a tool or background job its own handle. Closing the agent's
// handle must not disconnect a job that is still using the same SSH transport.
func (connection *Connection) Retain() (*Connection, error) {
	connection.shared.mutex.Lock()
	defer connection.shared.mutex.Unlock()

	if connection.shared.users == 0 {
		return nil, errors.New("SSH connection is closed")
	}

	connection.shared.users++

	return &Connection{Client: connection.Client, shared: connection.shared}, nil
}

func (connection *Connection) Ping(ctx context.Context) error {
	completed := make(chan error, 1)
	go func() {
		_, _, err := connection.Client.SendRequest("keepalive@openssh.com", true, nil)
		completed <- err
	}()

	select {
	case err := <-completed:
		return err
	case <-ctx.Done():
		// Close the transport itself: all retained handles must observe its loss.
		_ = connection.Client.Close()
		return ctx.Err()
	}
}

type Connector struct{ Store *datastore.Instance }

func (connector *Connector) Machine(id string) (domain.Machine, error) {
	var machine domain.Machine

	if parts := strings.SplitN(id, "/", 2); len(parts) == 2 {
		var pool domain.Pool
		if err := connector.Store.Load("pool", parts[0], &pool); err != nil {
			return machine, err
		}

		pool = WithPoolDefaults(pool)

		for _, candidate := range pool.Machines {
			if candidate.ID == parts[1] {
				return poolMachine(pool, id, candidate), nil
			}
		}

		return machine, errors.New("unknown pool machine")
	}

	err := connector.Store.Load("machine", id, &machine)

	return WithMachineDefaults(machine), err
}

func poolMachine(pool domain.Pool, id string, machine domain.Machine) domain.Machine {
	pool = WithPoolDefaults(pool)
	machine.ID, machine.WorkspaceRoot = id, pool.WorkspaceRoot

	if len(machine.Storage) == 0 {
		machine.Storage = pool.Storage
	}

	if len(machine.QuotaCommand) == 0 {
		machine.QuotaCommand = pool.QuotaCommand
	}

	if machine.Sandbox == "" {
		machine.Sandbox = pool.Sandbox
	}

	machine.SingleUser = machine.SingleUser || pool.SingleUser

	return WithMachineDefaults(machine)
}

func (connector *Connector) Dial(ctx context.Context, id string) (*Connection, error) {
	conn := &Connection{}
	visiting := map[string]bool{}
	var identities []verifiedHostIdentity
	var dial func(string, *ssh.Client) (*ssh.Client, error)
	dial = func(id string, via *ssh.Client) (*ssh.Client, error) {
		if visiting[id] || len(conn.chain) > 16 {
			return nil, errors.New("cyclic or excessive jump hosts")
		}

		visiting[id] = true
		defer delete(visiting, id)

		machine, err := connector.Machine(id)
		if err != nil {
			return nil, err
		}

		for _, jump := range machine.Jumps {
			via, err = dial(jump, via)
			if err != nil {
				return nil, err
			}
		}

		raw, err := connector.Store.PrivateKey(machine.KeyID)
		if err != nil {
			return nil, err
		}

		key, err := x509.ParseECPrivateKey(raw)
		if err != nil {
			return nil, err
		}

		signer, err := ssh.NewSignerFromKey(key)
		if err != nil {
			return nil, err
		}

		policy, err := connector.hostPolicy(id)
		if err != nil {
			return nil, err
		}

		cfg := &ssh.ClientConfig{
			User: machine.User,
			Auth: []ssh.AuthMethod{
				ssh.PublicKeys(signer),
			},
			Timeout: 15 * time.Second,
			HostKeyCallback: func(host string, address net.Addr, key ssh.PublicKey) error {
				identity := verifiedHostIdentity{machineID: id, address: host, remote: address, key: key}
				if err := connector.verifyHostIdentity(identity); err != nil {
					return err
				}

				identities = append(identities, identity)

				return nil
			},
		}

		addr := net.JoinHostPort(machine.Host, strconv.Itoa(machine.Port))

		if policy != nil {
			cfg.HostKeyAlgorithms = policy.algorithms(addr)
		}

		var socket net.Conn

		if via == nil {
			socket, err = (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, "tcp", addr)
		} else {
			socket, err = via.DialContext(ctx, "tcp", addr)
		}

		if err != nil {
			return nil, err
		}

		_ = socket.SetDeadline(time.Now().Add(20 * time.Second))
		stop := context.AfterFunc(ctx, func() { _ = socket.Close() })
		defer stop()

		clientConn, chans, reqs, err := ssh.NewClientConn(socket, addr, cfg)
		if err != nil {
			_ = socket.Close()
			return nil, err
		}

		_ = socket.SetDeadline(time.Time{})
		client := ssh.NewClient(clientConn, chans, reqs)
		conn.chain = append(conn.chain, client)

		return client, nil
	}

	client, err := dial(id, nil)
	if err != nil {
		conn.Close()
		return nil, err
	}

	conn.Client = client
	conn.shared = &sharedConnection{users: 1, chain: conn.chain, identities: identities}

	return conn, nil
}

func DecodePublicKey(data string) ([]byte, error) {
	key, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(data))
	if err != nil || len(rest) > 0 {
		return nil, errors.New("one SSH public key expected")
	}

	return key.Marshal(), nil
}

func KeyFingerprint(raw []byte) string {
	key, err := ssh.ParsePublicKey(raw)
	if err != nil {
		return "invalid"
	}

	return ssh.FingerprintSHA256(key)
}

func PublicKey(raw []byte) (string, error) {
	key, err := x509.ParseECPrivateKey(raw)
	if err != nil {
		return "", err
	}

	pub, err := ssh.NewPublicKey(&key.PublicKey)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))), nil
}

func Encode(data string) string {
	return base64.StdEncoding.EncodeToString([]byte(data))
}

// VerifyTrust applies later trust revocations to a reused SSH connection too.
func (connector *Connector) VerifyTrust(connection *Connection) error {
	for _, identity := range connection.shared.identities {
		if err := connector.verifyHostIdentity(identity); err != nil {
			return err
		}
	}

	return nil
}

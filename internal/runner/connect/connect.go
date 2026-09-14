// Package connect dials an SSH/SFTP session on a remote host and
// exposes the open connection as a small, no-leak Conn wrapper.
//
// The other runner modules (shell, copy, arch, ping) all build on
// top of Connect: each one opens a fresh Conn, runs its work, and
// closes the Conn before returning. Sharing the connection helper
// here keeps the SSH client config, host-key policy, and
// credential-lookup path in a single place.
package connect

import (
	"fmt"
	"net"
	"os"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"steel/internal/inventory"
)

// Conn bundles an open SSH client and its SFTP subsystem. Callers
// must invoke Close when done; otherwise both the SFTP subsystem and
// the underlying TCP socket leak.
type Conn struct {
	Client *ssh.Client
	SFTP   *sftp.Client
}

// Connect dials host h and authenticates. ssh_key wins over password
// when both are set. When ssh_key is empty we fall back to the local
// ssh-agent (handy for users who keep passphrases in the agent).
func Connect(h *inventory.Host) (*Conn, error) {
	methods, err := AuthMethods(h)
	if err != nil {
		return nil, err
	}

	cfg := &ssh.ClientConfig{
		User:            h.User,
		Auth:            methods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // steel is an ops tool for trusted fleets
		Timeout:         15 * time.Second,
	}

	addr := net.JoinHostPort(h.IP, fmt.Sprintf("%d", h.Port))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh %s@%s: %w", h.User, addr, err)
	}

	// ChannelMultiplexing is the SFTP default; explicit for clarity.
	sftpClient, err := sftp.NewClient(client, sftp.MaxPacket(32*1024))
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("sftp %s@%s: %w", h.User, addr, err)
	}

	return &Conn{Client: client, SFTP: sftpClient}, nil
}

// Close releases the SFTP subsystem and the underlying SSH client.
// Safe to call on a nil receiver.
func (c *Conn) Close() {
	if c == nil {
		return
	}
	if c.SFTP != nil {
		_ = c.SFTP.Close()
	}
	if c.Client != nil {
		_ = c.Client.Close()
	}
}

// AuthMethods returns the SSH auth methods to use for host h. The
// priority is: ssh_key (when set) → password (when set) → the
// local ssh-agent (when SSH_AUTH_SOCK is set). It returns an
// error only when none of these paths produce a usable method,
// so the caller can surface a clear "no credentials" message
// instead of ssh.Dial's generic "no auth methods" error.
//
// AuthMethods is exported because the ping probe needs the same
// lookup but with a different dial timeout — it would otherwise
// inherit connect's hardcoded 15s deadline, defeating
// `steel ping --timeout 1s`.
func AuthMethods(h *inventory.Host) ([]ssh.AuthMethod, error) {
	if h.SSHKey != "" {
		key, err := os.ReadFile(h.SSHKey)
		if err != nil {
			return nil, fmt.Errorf("read ssh_key %q: %w", h.SSHKey, err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("parse ssh_key %q: %w", h.SSHKey, err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	}

	if h.Password != "" {
		return []ssh.AuthMethod{ssh.Password(h.Password)}, nil
	}

	// No credentials in the inventory — try the local ssh-agent.
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			ag := agent.NewClient(conn)
			return []ssh.AuthMethod{ssh.PublicKeysCallback(ag.Signers)}, nil
		}
	}

	return nil, fmt.Errorf("host %q has no usable credentials", h.Name)
}

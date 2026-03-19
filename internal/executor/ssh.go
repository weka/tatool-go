package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// SSHExecutor implements Executor using SSH/SFTP.
type SSHExecutor struct {
	user       string
	authMethods []ssh.AuthMethod
	useDzdo    bool
	clients    map[string]*ssh.Client
	sftpClients map[string]*sftp.Client
}

// NewSSHExecutor creates an SSH executor with the given credentials.
func NewSSHExecutor(user, password, passwordEnv, passwordFile, keyFile string, useDzdo bool) (*SSHExecutor, error) {
	e := &SSHExecutor{
		user:        user,
		useDzdo:     useDzdo,
		clients:     make(map[string]*ssh.Client),
		sftpClients: make(map[string]*sftp.Client),
	}

	// Resolve password
	pw := resolvePassword(password, passwordEnv, passwordFile)

	// Build auth methods in priority order
	if keyFile != "" {
		am, err := publicKeyAuth(keyFile)
		if err != nil {
			return nil, fmt.Errorf("loading key %s: %w", keyFile, err)
		}
		e.authMethods = append(e.authMethods, am)
	}

	if pw != "" {
		e.authMethods = append(e.authMethods, ssh.Password(pw))
	}

	// Try SSH agent
	if agentAuth := sshAgentAuth(); agentAuth != nil {
		e.authMethods = append(e.authMethods, agentAuth)
	}

	if len(e.authMethods) == 0 {
		// Try default keys as last resort
		for _, name := range []string{"id_rsa", "id_ed25519", "id_ecdsa"} {
			home, _ := os.UserHomeDir()
			path := filepath.Join(home, ".ssh", name)
			if am, err := publicKeyAuth(path); err == nil {
				e.authMethods = append(e.authMethods, am)
			}
		}
	}

	if len(e.authMethods) == 0 {
		return nil, fmt.Errorf("no SSH authentication methods available")
	}

	return e, nil
}

func resolvePassword(direct, envVar, file string) string {
	if direct != "" {
		return direct
	}
	if envVar != "" {
		return os.Getenv(envVar)
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
}

func publicKeyAuth(keyPath string) (ssh.AuthMethod, error) {
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, err
	}
	return ssh.PublicKeys(signer), nil
}

func sshAgentAuth() ssh.AuthMethod {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil
	}
	return ssh.PublicKeysCallback(agent.NewClient(conn).Signers)
}

// SSHClientConfig returns an ssh.ClientConfig for use in discovery.
func (e *SSHExecutor) SSHClientConfig() *ssh.ClientConfig {
	return &ssh.ClientConfig{
		User:            e.user,
		Auth:            e.authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
}

func (e *SSHExecutor) connect(ip string) (*ssh.Client, *sftp.Client, error) {
	if client, ok := e.clients[ip]; ok {
		return client, e.sftpClients[ip], nil
	}

	config := &ssh.ClientConfig{
		User:            e.user,
		Auth:            e.authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	client, err := ssh.Dial("tcp", net.JoinHostPort(ip, "22"), config)
	if err != nil {
		return nil, nil, fmt.Errorf("SSH dial %s: %w", ip, err)
	}

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		client.Close()
		return nil, nil, fmt.Errorf("SFTP to %s: %w", ip, err)
	}

	e.clients[ip] = client
	e.sftpClients[ip] = sftpClient

	return client, sftpClient, nil
}

// CopyScripts uploads all scripts to /tmp/ta/ on the target via SFTP.
func (e *SSHExecutor) CopyScripts(ctx context.Context, ip string, scripts fs.FS) error {
	_, sftpClient, err := e.connect(ip)
	if err != nil {
		return err
	}

	sftpClient.MkdirAll(remoteScriptDir)

	return fs.WalkDir(scripts, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		data, err := fs.ReadFile(scripts, path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		remotePath := filepath.Join(remoteScriptDir, filepath.Base(path))
		f, err := sftpClient.Create(remotePath)
		if err != nil {
			return fmt.Errorf("creating remote %s: %w", remotePath, err)
		}
		defer f.Close()

		if _, err := f.Write(data); err != nil {
			return fmt.Errorf("writing remote %s: %w", remotePath, err)
		}

		if err := sftpClient.Chmod(remotePath, 0755); err != nil {
			return fmt.Errorf("chmod %s: %w", remotePath, err)
		}

		return nil
	})
}

// Exec runs a single script on the target via SSH.
func (e *SSHExecutor) Exec(ctx context.Context, ip string, scriptPath string, useDzdo bool) (ExecResult, error) {
	client, _, err := e.connect(ip)
	if err != nil {
		return ExecResult{}, err
	}

	session, err := client.NewSession()
	if err != nil {
		return ExecResult{}, fmt.Errorf("new SSH session to %s: %w", ip, err)
	}
	defer session.Close()

	remotePath := filepath.Join(remoteScriptDir, scriptPath)
	sudo := "sudo"
	if e.useDzdo || useDzdo {
		sudo = "dzdo"
	}

	cmd := fmt.Sprintf("%s bash -c 'chmod +x %s && %s'", sudo, remotePath, remotePath)

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	err = session.Run(cmd)
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			exitCode = exitErr.ExitStatus()
		} else {
			return ExecResult{
				Stdout:   stdout.String(),
				Stderr:   stderr.String(),
				ExitCode: 1,
				Status:   StatusFail,
			}, fmt.Errorf("exec on %s: %w", ip, err)
		}
	}

	return ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
		Status:   StatusFromExitCode(exitCode),
	}, nil
}

// Cleanup removes the deployed scripts from the target.
func (e *SSHExecutor) Cleanup(ctx context.Context, ip string) error {
	client, _, err := e.connect(ip)
	if err != nil {
		return err
	}

	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	sudo := "sudo"
	if e.useDzdo {
		sudo = "dzdo"
	}

	return session.Run(fmt.Sprintf("%s rm -rf %s", sudo, remoteScriptDir))
}

// FetchDiagnostics collects /tmp/diagnostics* from the target via SFTP.
func (e *SSHExecutor) FetchDiagnostics(ctx context.Context, ip string, localDir string) error {
	client, sftpClient, err := e.connect(ip)
	if err != nil {
		return err
	}

	// Find diagnostics directory
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	var findOut bytes.Buffer
	session.Stdout = &findOut
	session.Run("find /tmp -maxdepth 1 -type d -name '*diagnostics*' 2>/dev/null | head -n 1")
	session.Close()

	diagDir := strings.TrimSpace(findOut.String())
	if diagDir == "" {
		return nil // no diagnostics
	}

	// Create local target directory
	localDiagDir := filepath.Join(localDir, fmt.Sprintf("%s_diagnostics", ip))
	os.MkdirAll(localDiagDir, 0755)

	// Walk remote diagnostics and download
	walker := sftpClient.Walk(diagDir)
	for walker.Step() {
		if walker.Err() != nil {
			continue
		}
		if walker.Stat().IsDir() {
			continue
		}

		remotePath := walker.Path()
		localPath := filepath.Join(localDiagDir, filepath.Base(remotePath))

		remoteFile, err := sftpClient.Open(remotePath)
		if err != nil {
			continue
		}

		localFile, err := os.Create(localPath)
		if err != nil {
			remoteFile.Close()
			continue
		}

		io.Copy(localFile, remoteFile)
		localFile.Close()
		remoteFile.Close()
	}

	// Remove remote diagnostics
	cleanSession, err := client.NewSession()
	if err == nil {
		sudo := "sudo"
		if e.useDzdo {
			sudo = "dzdo"
		}
		cleanSession.Run(fmt.Sprintf("%s rm -rf %s", sudo, diagDir))
		cleanSession.Close()
	}

	return nil
}

// Close releases all SSH and SFTP connections.
func (e *SSHExecutor) Close() error {
	for ip, sc := range e.sftpClients {
		sc.Close()
		delete(e.sftpClients, ip)
	}
	for ip, c := range e.clients {
		c.Close()
		delete(e.clients, ip)
	}
	return nil
}

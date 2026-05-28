package ssh

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"stackforge/internal/stackforge/remoteexec"
)

type Executor struct {
	User           string
	Port           int
	PrivateKeyPath string
	Password       string
	Timeout        time.Duration
}

func NewExecutor(user string, port int, keyPath string) *Executor {
	if port == 0 {
		port = 22
	}
	return &Executor{User: user, Port: port, PrivateKeyPath: keyPath, Timeout: 30 * time.Second}
}

func NewPasswordExecutor(user string, port int, password string) *Executor {
	if port == 0 {
		port = 22
	}
	return &Executor{User: user, Port: port, Password: password, Timeout: 30 * time.Second}
}

func (e *Executor) Run(ctx context.Context, node string, cmd remoteexec.Command) (remoteexec.Result, error) {
	auth := []ssh.AuthMethod{}
	if e.PrivateKeyPath != "" {
		key, err := os.ReadFile(expandHome(e.PrivateKeyPath))
		if err != nil {
			return remoteexec.Result{}, err
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return remoteexec.Result{}, err
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if e.Password != "" {
		auth = append(auth, ssh.Password(e.Password))
		cmd.Secrets = append(cmd.Secrets, e.Password)
	}
	if len(auth) == 0 {
		return remoteexec.Result{}, fmt.Errorf("ssh auth method is required")
	}
	dialTimeout := e.Timeout
	if cmd.Timeout > 0 {
		dialTimeout = cmd.Timeout
	}
	clientConfig := &ssh.ClientConfig{
		User:            e.User,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         dialTimeout,
	}
	addr := net.JoinHostPort(node, fmt.Sprint(e.Port))
	client, err := ssh.Dial("tcp", addr, clientConfig)
	if err != nil {
		return remoteexec.Result{}, err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return remoteexec.Result{}, err
	}
	defer session.Close()
	command := cmd.Command
	if cmd.Sudo && e.User != "root" {
		command = "sudo -n " + command
	}
	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()
	runCtx := ctx
	var cancel context.CancelFunc
	if cmd.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, cmd.Timeout)
	} else if e.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, e.Timeout)
	}
	if cancel != nil {
		defer cancel()
	}
	select {
	case <-runCtx.Done():
		_ = session.Signal(ssh.SIGKILL)
		return remoteexec.Result{}, runCtx.Err()
	case err := <-done:
		res := remoteexec.Result{Stdout: remoteexec.Redact(stdout.String(), cmd.Secrets), Stderr: remoteexec.Redact(stderr.String(), cmd.Secrets)}
		if err != nil {
			if exit, ok := err.(*ssh.ExitError); ok {
				res.ExitCode = exit.ExitStatus()
			}
			return res, err
		}
		return res, nil
	}
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return home + path[1:]
	}
	return path
}

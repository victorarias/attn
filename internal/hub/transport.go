package hub

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

const remoteWSMessageReadLimit = 8 << 20
const remoteWSDialRetryDelay = 250 * time.Millisecond

func connectViaSSH(ctx context.Context, sshTarget, authToken string) (*websocket.Conn, *exec.Cmd, error) {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		ws, cmd, err := connectViaSSHOnce(ctx, sshTarget, authToken)
		if err == nil {
			return ws, cmd, nil
		}
		lastErr = err
		if ctx.Err() != nil || !isRetryableWSRelayDialError(err) || attempt == 4 {
			return nil, nil, err
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(remoteWSDialRetryDelay):
		}
	}
	return nil, nil, lastErr
}

func connectViaSSHOnce(ctx context.Context, sshTarget, authToken string) (*websocket.Conn, *exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, "ssh", append(sshBaseArgs(sshTarget), remoteShellCommand(remoteAttnCommand("ws-relay")))...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, nil, err
	}

	var (
		acceptOnce sync.Once
		acceptErr  error
	)

	go func() {
		conn, err := ln.Accept()
		acceptOnce.Do(func() {
			acceptErr = err
		})
		_ = ln.Close()
		if err != nil {
			return
		}
		go func() {
			_, _ = io.Copy(stdin, conn)
			_ = stdin.Close()
		}()
		go func() {
			_, _ = io.Copy(conn, stdout)
			_ = conn.Close()
		}()
	}()

	headers := http.Header{}
	if token := strings.TrimSpace(authToken); token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}

	url := fmt.Sprintf("ws://127.0.0.1:%d/ws", ln.Addr().(*net.TCPAddr).Port)
	ws, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		_ = ln.Close()
		_ = cmd.Process.Kill()
		if acceptErr != nil {
			return nil, nil, acceptErr
		}
		return nil, nil, err
	}
	ws.SetReadLimit(remoteWSMessageReadLimit)
	return ws, cmd, nil
}

func isRetryableWSRelayDialError(err error) bool {
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(message, "eof") ||
		strings.Contains(message, "connection reset by peer") ||
		strings.Contains(message, "connection refused")
}

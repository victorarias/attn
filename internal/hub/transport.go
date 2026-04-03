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

	"nhooyr.io/websocket"
)

func connectViaSSH(ctx context.Context, sshTarget, authToken string) (*websocket.Conn, *exec.Cmd, error) {
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
	return ws, cmd, nil
}

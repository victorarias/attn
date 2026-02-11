package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const daemonIDFileName = "daemon-id"

func ensureDaemonInstanceID(dataRoot string) (string, error) {
	if strings.TrimSpace(dataRoot) == "" {
		return "", fmt.Errorf("missing data root")
	}
	if err := os.MkdirAll(dataRoot, 0700); err != nil {
		return "", fmt.Errorf("create data root: %w", err)
	}

	idPath := filepath.Join(dataRoot, daemonIDFileName)
	lockPath := idPath + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", fmt.Errorf("open daemon id lock: %w", err)
	}
	defer lockFile.Close()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return "", fmt.Errorf("lock daemon id file: %w", err)
	}
	defer func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	}()

	if data, err := os.ReadFile(idPath); err == nil {
		id := strings.TrimSpace(string(data))
		if validDaemonInstanceID(id) {
			return id, nil
		}
	}

	id, err := newDaemonInstanceID()
	if err != nil {
		return "", err
	}

	tmpPath := fmt.Sprintf("%s.tmp.%d.%d", idPath, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, []byte(id+"\n"), 0600); err != nil {
		return "", fmt.Errorf("write temp daemon id: %w", err)
	}
	if err := os.Rename(tmpPath, idPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("persist daemon id: %w", err)
	}
	return id, nil
}

func newDaemonInstanceID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate daemon id: %w", err)
	}
	return "d-" + hex.EncodeToString(buf), nil
}

func validDaemonInstanceID(id string) bool {
	if !strings.HasPrefix(id, "d-") {
		return false
	}
	if len(id) != 34 {
		return false
	}
	_, err := hex.DecodeString(id[2:])
	return err == nil
}

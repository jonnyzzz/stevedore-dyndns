//go:build integration

package caddy

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func tempDir(t *testing.T, prefix string) string {
	t.Helper()

	dir, err := os.MkdirTemp("/tmp", prefix)
	if err != nil {
		return t.TempDir()
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func waitForAccessLogEntry(t *testing.T, containerID, needle string, timeout time.Duration) error {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var lastLogs string

	for time.Now().Before(deadline) {
		logs, err := getContainerLogs(containerID)
		if err == nil {
			lastLogs = logs
			if strings.Contains(logs, needle) {
				return nil
			}
		} else {
			t.Logf("Failed to read container logs: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("access log entry %q not found within %s\nlogs:\n%s", needle, timeout, lastLogs)
}

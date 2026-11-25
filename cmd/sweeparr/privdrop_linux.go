//go:build linux

package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func firstNonEmptyEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// dropPrivilegesIfConfigured checks SWEEPARR_UID/PUID and SWEEPARR_GID/PGID.
// When the process starts as root (UID 0) or a different UID/GID and target IDs are configured,
// it ensures stateDir ownership and drops process GID, supplemental groups, and UID across all OS threads.
func dropPrivilegesIfConfigured(stateDir string) error {
	curUID := os.Getuid()
	curGID := os.Getgid()

	uidStr := firstNonEmptyEnv("SWEEPARR_UID", "PUID")
	gidStr := firstNonEmptyEnv("SWEEPARR_GID", "PGID")
	if uidStr == "" && gidStr == "" {
		return nil
	}

	targetUID := curUID
	targetGID := curGID
	if uidStr != "" {
		u, err := strconv.Atoi(uidStr)
		if err != nil || u < 0 {
			return fmt.Errorf("invalid target UID %q", uidStr)
		}
		targetUID = u
	}
	if gidStr != "" {
		g, err := strconv.Atoi(gidStr)
		if err != nil || g < 0 {
			return fmt.Errorf("invalid target GID %q", gidStr)
		}
		targetGID = g
	}

	if curUID == 0 && stateDir != "" {
		_ = os.MkdirAll(stateDir, 0o700)
		_ = os.Chown(stateDir, targetUID, targetGID)
		if entries, err := os.ReadDir(stateDir); err == nil {
			for _, e := range entries {
				_ = os.Chown(filepath.Join(stateDir, e.Name()), targetUID, targetGID)
			}
		}
	}

	if targetGID != curGID {
		if err := syscall.Setgroups([]int{targetGID}); err != nil && curUID == 0 {
			return fmt.Errorf("setgroups(%d): %w", targetGID, err)
		}
		if err := syscall.Setresgid(targetGID, targetGID, targetGID); err != nil {
			return fmt.Errorf("setresgid(%d): %w", targetGID, err)
		}
	}
	if targetUID != curUID {
		if err := syscall.Setresuid(targetUID, targetUID, targetUID); err != nil {
			return fmt.Errorf("setresuid(%d): %w", targetUID, err)
		}
	}

	if targetUID != curUID || targetGID != curGID {
		slog.Info("dropped process privileges", "uid", os.Getuid(), "gid", os.Getgid())
	}
	return nil
}

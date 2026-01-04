//go:build windows

package launcher

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/mitchellh/go-ps"
)

func killGroup(pid int) {
	terminateProcess(pid)
}

func (l *Launcher) osSetupCmd(ctx context.Context, cmd *exec.Cmd, uid, gid int) {
	cmd.SysProcAttr = getSysProcAttr(uid, gid)
	if l.Browser.HideWindow {
		cmd.SysProcAttr.HideWindow = true
	}
}

func getSysProcAttr(uid, gid int) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

func terminateProcess(pid int) {
	handle, err := syscall.OpenProcess(syscall.PROCESS_TERMINATE, true, uint32(pid))
	if err != nil {
		return
	}

	_ = syscall.TerminateProcess(handle, 0)
	_ = syscall.CloseHandle(handle)
}

// getWindowsCmdPath tries to find the cmd.exe path on Windows
func getWindowsCmdPath() string {
	cmdPath := os.Getenv("COMSPEC")
	if len(cmdPath) != 0 {
		return cmdPath
	}

	winDir := os.Getenv("WINDIR")
	if len(winDir) != 0 {
		return winDir + "\\System32\\cmd.exe"
	}

	rootDir := os.Getenv("SystemRoot")
	if len(rootDir) != 0 {
		return rootDir + "\\System32\\cmd.exe"
	}

	return "C:\\Windows\\System32\\cmd.exe"
}

func killLeftoverProcesses(pid int, bin string) {
	// Terminate the process group under the original pid
	procs, _ := ps.Processes()
	for _, proc := range procs {
		if (proc.Pid() == pid || proc.PPid() == pid) && strings.EqualFold(filepath.Base(proc.Executable()), bin) {
			// Carefully limit the taskkill to this executable name and this child pid
			cpid := strconv.FormatInt(int64(proc.Pid()), 10)
			cmd := exec.Command(getWindowsCmdPath(), "/c", "taskkill /F /T /IM "+bin+" /PID "+cpid) //nolint:gosec
			cmd.Stdin = nil
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			_ = cmd.Run()
		}
	}
	terminateProcess(pid)
}

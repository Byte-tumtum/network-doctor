//go:build windows

package ui

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsJob struct {
	mu     sync.Mutex
	handle windows.Handle
}

func startProcess(cmd *exec.Cmd) (func(), error) {
	job, err := newWindowsJob()
	if err != nil {
		return nil, err
	}

	job.mu.Lock()
	cmd.Cancel = job.terminate
	attr := new(syscall.SysProcAttr)
	if cmd.SysProcAttr != nil {
		*attr = *cmd.SysProcAttr
	}
	attr.CreationFlags |= windows.CREATE_SUSPENDED
	cmd.SysProcAttr = attr

	if err := cmd.Start(); err != nil {
		job.mu.Unlock()
		job.close()
		return nil, err
	}
	if err := assignAndResume(job.handle, uint32(cmd.Process.Pid)); err != nil {
		// The process has never run, so it cannot have escaped the job. Cover
		// both sides of an assignment failure, reap it, and return the setup
		// error rather than its forced exit status.
		_ = windows.TerminateJobObject(job.handle, 1)
		_ = cmd.Process.Kill()
		job.mu.Unlock()
		_ = cmd.Wait()
		job.close()
		return nil, err
	}
	job.mu.Unlock()
	return job.close, nil
}

func newWindowsJob() (*windowsJob, error) {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		handle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	return &windowsJob{handle: handle}, nil
}

func assignAndResume(job windows.Handle, pid uint32) error {
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		pid,
	)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		return err
	}

	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)

	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		_, err = windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		return err
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return err
	}
	return windows.ERROR_NOT_FOUND
}

func (j *windowsJob) terminate() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle == 0 {
		return os.ErrProcessDone
	}
	return windows.TerminateJobObject(j.handle, 1)
}

func (j *windowsJob) close() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle != 0 {
		windows.CloseHandle(j.handle)
		j.handle = 0
	}
}

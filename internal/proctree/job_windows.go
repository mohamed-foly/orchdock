//go:build windows

package proctree

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Job is a Windows Job Object with kill-on-close.
type Job struct{ h windows.Handle }

// NewJob creates an anonymous job that kills all its members when its last
// handle closes, which also happens if the host process dies.
func NewJob() (*Job, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(h)
		return nil, fmt.Errorf("set job limits: %w", err)
	}
	return &Job{h: h}, nil
}

// Assign adds the process with the given id to the job.
func (j *Job) Assign(pid int) error {
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("open process %d: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(ph) }()
	if err := windows.AssignProcessToJobObject(j.h, ph); err != nil {
		return fmt.Errorf("assign process %d to job: %w", pid, err)
	}
	return nil
}

// Terminate ends every process in the job.
func (j *Job) Terminate() error { return windows.TerminateJobObject(j.h, 1) }

// Kill ends every process in the job.
func (j *Job) Kill() error { return windows.TerminateJobObject(j.h, 1) }

// Close closes the job handle; remaining members are killed.
func (j *Job) Close() error { return windows.CloseHandle(j.h) }

// ResumeProcess resumes every thread of a process created with
// CREATE_SUSPENDED. The PTY library closes the primary thread handle itself,
// so the threads are found through a Toolhelp snapshot.
func ResumeProcess(pid int) (int, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return 0, fmt.Errorf("thread snapshot: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	var te windows.ThreadEntry32
	te.Size = uint32(unsafe.Sizeof(te))
	resumed := 0
	for err = windows.Thread32First(snap, &te); err == nil; err = windows.Thread32Next(snap, &te) {
		if te.OwnerProcessID != uint32(pid) {
			continue
		}
		th, oerr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, te.ThreadID)
		if oerr != nil {
			continue
		}
		if _, rerr := windows.ResumeThread(th); rerr == nil {
			resumed++
		}
		_ = windows.CloseHandle(th)
	}
	if resumed == 0 {
		return 0, fmt.Errorf("no thread of process %d could be resumed", pid)
	}
	return resumed, nil
}

var (
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procIsProcessInJob        = kernel32.NewProc("IsProcessInJob")
	procSetConsoleCtrlHandler = kernel32.NewProc("SetConsoleCtrlHandler")
)

// SelfInJob reports whether this process belongs to any job.
func SelfInJob() bool {
	var in int32
	r, _, _ := procIsProcessInJob.Call(uintptr(windows.CurrentProcess()), 0, uintptr(unsafe.Pointer(&in)))
	return r != 0 && in != 0
}

// SelfJobLimits returns the limit flags of the job this process is in.
func SelfJobLimits() (uint32, error) {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	err := windows.QueryInformationJobObject(0, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil)
	return info.BasicLimitInformation.LimitFlags, err
}

// ClearIgnoreCtrlC calls SetConsoleCtrlHandler(NULL, FALSE). A process started
// with CREATE_NEW_PROCESS_GROUP ignores Ctrl+C and passes that on to its
// children; without this call a Ctrl+C written into the PTY does nothing.
func ClearIgnoreCtrlC() error {
	r, _, err := procSetConsoleCtrlHandler.Call(0, 0)
	if r == 0 {
		return err
	}
	return nil
}

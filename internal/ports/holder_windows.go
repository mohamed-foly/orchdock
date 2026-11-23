package ports

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iphlpapi            = windows.NewLazySystemDLL("iphlpapi.dll")
	getExtendedTCPTable = iphlpapi.NewProc("GetExtendedTcpTable")
)

const (
	afInet                 = 2
	tcpTableOwnerPIDListen = 3
	rowSize                = 24 // MIB_TCPROW_OWNER_PID: state, local addr, local port, remote addr, remote port, pid
)

// Holder names the process listening on the TCP port (IPv4), like
// "backlog.exe (pid 1234)", or "" when it cannot be told.
func Holder(port int) string {
	pid := listenerPID(port)
	if pid == 0 {
		return ""
	}
	name := processName(pid)
	if name == "" {
		return fmt.Sprintf("pid %d", pid)
	}
	return fmt.Sprintf("%s (pid %d)", name, pid)
}

func listenerPID(port int) uint32 {
	var size uint32
	_, _, _ = getExtendedTCPTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, afInet, tcpTableOwnerPIDListen, 0)
	if size == 0 {
		return 0
	}
	buf := make([]byte, size+1024)
	size = uint32(len(buf))
	r, _, _ := getExtendedTCPTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, afInet, tcpTableOwnerPIDListen, 0)
	if r != 0 {
		return 0
	}
	n := binary.LittleEndian.Uint32(buf[:4])
	for i := uint32(0); i < n; i++ {
		row := buf[4+i*rowSize : 4+(i+1)*rowSize]
		// the port is in network byte order in the low 16 bits
		p := int(binary.BigEndian.Uint16(row[8:10]))
		if p == port {
			return binary.LittleEndian.Uint32(row[20:24])
		}
	}
	return 0
}

func processName(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer func() { _ = windows.CloseHandle(h) }()
	buf := make([]uint16, windows.MAX_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return filepath.Base(windows.UTF16ToString(buf[:n]))
}

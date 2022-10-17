package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/k0sproject/k0s/pkg/supervisor"
)

func main() {
	var pidFlag = flag.Int("pid", -1, "PID to mess around with")
	flag.Parse()

	if *pidFlag < 1 {
		fmt.Fprintln(os.Stderr, "Invalid PID:", *pidFlag)
		os.Exit(1)
	}

	// x := supervisor.Supervisor{
	// 	Name:    `sh`,
	// 	BinPath: `sh`,
	// 	RunDir:  `C:\tools\msys64\usr\bin`,
	// 	DataDir: `C:\Users\twieczorek`,
	// 	Args:    []string{"-xc", "while sleep 1; do echo foo; done"},
	// }

	// defer x.Stop()
	// if err := x.Supervise(); err != nil {
	// 	fmt.Fprintln(os.Stderr, "Failed to spawn cat: ", err)
	// 	os.Exit(1)
	// }

	// time.Sleep(5 * time.Second)

	// pid := uint32(x.GetProcess().Pid)
	pid := uint32(*pidFlag)
	fmt.Fprintln(os.Stderr, "Attaching to ", pid)

	if err := supervisor.FreeConsole(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	err := supervisor.AttachConsole(pid)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to attach to", pid, ":", err)
		// os.Exit(1)
	} else {
		fmt.Fprintln(os.Stderr, "Attached to", pid)
	}

	event := supervisor.ConsoleCTRLBREAK
	if err := supervisor.GenerateConsoleCtrlEvent(0, event); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to send", event, "to", pid, ":", err)
		// os.Exit(1)
	} else {
		fmt.Fprintln(os.Stderr, "Sent", event, "to", pid)
	}
}

package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"time"
)

func init() {
	register("netcheck", command{
		Summary:  "Check which addresses the sandbox can connect to, which proves its network policy is enforced",
		Internal: true,
		Run:      runNetcheck,
	})
}

// runNetcheck dials every address and exits non-zero when one answers differently than expected
// Each line of its output is "reachable <addr>" or "unreachable <addr>", so the adapter can explain a failure
func runNetcheck(args []string) int {
	flags := flag.NewFlagSet("ump netcheck", flag.ContinueOnError)
	var reach, unreachable []string
	flags.Func("reach", "an address that must accept connections, may repeat", func(v string) error { reach = append(reach, v); return nil })
	flags.Func("unreachable", "an address that must not accept connections, may repeat", func(v string) error { unreachable = append(unreachable, v); return nil })
	timeout := flags.Duration("timeout", 3*time.Second, "how long each connection attempt may take")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	// Dial everything, and remember whether any answer broke the expectation
	ok := true
	check := func(addr string, want bool) {
		conn, err := net.DialTimeout("tcp", addr, *timeout)
		got := err == nil
		if got {
			_ = conn.Close()
		}
		state := "unreachable"
		if got {
			state = "reachable"
		}
		_, _ = fmt.Fprintln(os.Stdout, state, addr)
		ok = ok && got == want
	}
	for _, addr := range reach {
		check(addr, true)
	}
	for _, addr := range unreachable {
		check(addr, false)
	}
	if !ok {
		return 1
	}
	return 0
}

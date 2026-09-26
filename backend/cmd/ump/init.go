package main

import (
	"errors"
	"flag"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// resolvConf is where the sandbox's resolvers are configured
var resolvConf = "/etc/resolv.conf"

func init() {
	register("init", command{
		Summary:  "Keep the sandbox alive as its init process and reap orphaned processes",
		Internal: true,
		Run:      runInit,
	})
}

// runInit is the sandbox entrypoint and does what `tini -- sleep infinity` would, plus an optional hard lifetime
// Running the injected ump as the entrypoint means any OCI image works as a sandbox, even one without tini or a shell
func runInit(args []string) int {
	flags := flag.NewFlagSet("ump init", flag.ContinueOnError)
	ttl := flags.Duration("ttl", 0, "stop the sandbox after this long; zero means never")
	install := flags.Bool("install", false, "first install ump at "+umpBinaryPath+" and create the sandbox layout, for adapters that cannot copy them in before the sandbox starts")
	proxied := flags.Bool("proxied", false, "with --install, send SSH through the egress proxy")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	// A sandbox without its layout is useless, so a failure ends it right away and the adapter sees why
	if *install {
		if err := installLayout(*proxied); err != nil {
			errorf("init", "failed to set up the sandbox: %v", err)
			return 1
		}
	}

	// Resolvers the adapter chose replace Docker's embedded DNS before any command runs, since gVisor can't reach it
	if servers := os.Getenv("UMP_DNS"); servers != "" {
		if err := setResolvers(resolvConf, strings.Split(servers, ",")); err != nil {
			errorf("init", "failed to set the DNS servers: %v", err)
		}
	}

	// Adopt orphans even when something else is PID 1, e.g. under docker run --init
	becomeSubreaper()
	signals := make(chan os.Signal, 32)
	signal.Notify(signals, syscall.SIGCHLD, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	// Enforce the TTL by exiting, which makes the kernel kill everything else in the sandbox
	var expired <-chan time.Time
	if *ttl > 0 {
		expired = time.After(*ttl)
	}

	for {
		select {
		case s := <-signals:
			if s == syscall.SIGCHLD {
				reapChildren()
				continue
			}
			return 0
		case <-expired:
			errorf("init", "sandbox reached its time limit of %s", *ttl)
			return 0
		}
	}
}

// reapChildren collects every exited child, since SIGCHLD deliveries coalesce
func reapChildren() {
	for {
		var status syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &status, syscall.WNOHANG, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if pid <= 0 || err != nil {
			return
		}
	}
}

// setResolvers replaces the nameservers of a resolv.conf, keeping its search domains and options
func setResolvers(path string, servers []string) error {
	current, err := os.ReadFile(path) // #nosec G304 -- the path is the fixed resolv.conf, only tests pass another
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var b strings.Builder
	for _, server := range servers {
		if server = strings.TrimSpace(server); server != "" {
			b.WriteString("nameserver " + server + "\n")
		}
	}
	for line := range strings.Lines(string(current)) {
		fields := strings.Fields(line)
		if len(fields) > 0 && (fields[0] == "search" || fields[0] == "options") {
			b.WriteString(strings.TrimSpace(line) + "\n")
		}
	}

	// The file is written in place, since Docker bind-mounts it and a rename would fail
	return os.WriteFile(path, []byte(b.String()), 0o644) // #nosec G306 -- resolv.conf must be readable by every user
}

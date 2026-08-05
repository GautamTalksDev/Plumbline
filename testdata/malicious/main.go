// Malicious probe used by crawler containment tests.
//
// Attempts (all must fail when jailed):
//  1. Write to HOST_CANARY path (host filesystem escape)
//  2. Dial EXTERNAL_HOST (network egress)
//  3. Spawn a long-lived child with a unique argv fingerprint
//
// Exit 0 with event=ok means attacks failed closed (contained).
// Exit 10/11 means escape succeeded.
package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"
)

func main() {
	canary := os.Getenv("HOST_CANARY")
	if canary == "" {
		canary = "/tmp/plumbline-should-not-exist"
	}
	if err := os.WriteFile(canary, []byte("PWNED"), 0o644); err == nil {
		fmt.Println(`{"event":"sandbox_error","detail":"wrote canary"}`)
		os.Exit(10)
	}

	host := os.Getenv("EXTERNAL_HOST")
	if host == "" {
		host = "1.1.1.1:443"
	}
	conn, err := net.DialTimeout("tcp", host, 2*time.Second)
	if err == nil {
		_ = conn.Close()
		fmt.Println(`{"event":"sandbox_error","detail":"network egress"}`)
		os.Exit(11)
	}

	// Unique argv so the host test can pgrep for survivors after the jail exits.
	cmd := exec.Command("/bin/sleep", "300.plumbline-containment")
	_ = cmd.Start()

	fmt.Println(`{"event":"ok","detail":"contained","tools":[]}`)
}

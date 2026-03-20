package discovery

import (
	"os/exec"
	"strings"
)

// WekaBackendIPs runs the local weka CLI to get backend node IPs on this cluster.
// It returns nil if the weka CLI is not installed or returns no output.
func WekaBackendIPs() []string {
	out, err := exec.Command("weka", "cluster", "container", "-b", "--no-header").Output()
	if err != nil {
		return nil
	}
	// Each line is one container; column 4 is the IP. Keep only drives0 rows
	// (one per backend node) to avoid duplicates from compute/frontend containers.
	var ips []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		// fields[1] is container name (e.g. "drives0"), fields[3] is IP
		if strings.HasPrefix(fields[1], "drives0") {
			ip := fields[3]
			if ip != "" && !seen[ip] {
				seen[ip] = true
				ips = append(ips, ip)
			}
		}
	}
	return ips
}

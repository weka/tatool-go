package discovery

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// DiscoverK8sPods finds pods matching the Weka cluster naming pattern.
func DiscoverK8sPods(ctx context.Context, namespace, clusterName, kubeconfigPath string) ([]string, error) {
	cfg, err := buildConfig(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("building k8s config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating k8s clientset: %w", err)
	}

	podList, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}

	pattern := regexp.MustCompile(fmt.Sprintf(`^(%s-drive|%s-compute)`, regexp.QuoteMeta(clusterName), regexp.QuoteMeta(clusterName)))

	var pods []string
	for _, pod := range podList.Items {
		if pattern.MatchString(pod.Name) {
			pods = append(pods, pod.Name)
		}
	}

	sort.Strings(pods)

	if len(pods) == 0 {
		return nil, fmt.Errorf("no pods found matching pattern %s-(drive|compute)* in namespace %s", clusterName, namespace)
	}

	return pods, nil
}

// ExpandIPsFromSeed connects to a single seed IP and discovers all Weka backend IPs.
func ExpandIPsFromSeed(seedIP string, sshConfig *ssh.ClientConfig) ([]string, error) {
	client, err := ssh.Dial("tcp", net.JoinHostPort(seedIP, "22"), sshConfig)
	if err != nil {
		return nil, fmt.Errorf("SSH to seed %s: %w", seedIP, err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("SSH session to %s: %w", seedIP, err)
	}
	defer session.Close()

	var stdout bytes.Buffer
	session.Stdout = &stdout
	cmd := "weka cluster container -b --no-header | grep drives0 | awk '{print $4}'"
	if err := session.Run(cmd); err != nil {
		return nil, fmt.Errorf("weka command on %s: %w", seedIP, err)
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		return []string{seedIP}, nil
	}

	var ips []string
	for _, line := range strings.Split(output, "\n") {
		ip := strings.TrimSpace(line)
		if ip != "" {
			ips = append(ips, ip)
		}
	}

	if len(ips) == 0 {
		return []string{seedIP}, nil
	}

	return ips, nil
}

// NewSSHClientConfig creates an ssh.ClientConfig (used by ExpandIPsFromSeed).
func NewSSHClientConfig(user string, authMethods []ssh.AuthMethod) *ssh.ClientConfig {
	return &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
}

func buildConfig(kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	}
	if kc := os.Getenv("KUBECONFIG"); kc != "" {
		return clientcmd.BuildConfigFromFlags("", kc)
	}
	if home, _ := os.UserHomeDir(); home != "" {
		defaultKC := filepath.Join(home, ".kube", "config")
		if _, err := os.Stat(defaultKC); err == nil {
			return clientcmd.BuildConfigFromFlags("", defaultKC)
		}
	}
	return rest.InClusterConfig()
}

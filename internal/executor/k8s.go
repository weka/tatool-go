package executor

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

const remoteScriptDir = "/tmp/ta"
const execTimeout = 5 * time.Minute

// K8sExecutor implements Executor using client-go.
type K8sExecutor struct {
	clientset  *kubernetes.Clientset
	restConfig *rest.Config
	namespace  string
}

// NewK8sExecutor creates a K8s executor from kubeconfig path or in-cluster config.
func NewK8sExecutor(namespace, kubeconfigPath string) (*K8sExecutor, error) {
	var cfg *rest.Config
	var err error

	if kubeconfigPath != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	} else if kc := os.Getenv("KUBECONFIG"); kc != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", kc)
	} else if home, _ := os.UserHomeDir(); home != "" {
		defaultKC := filepath.Join(home, ".kube", "config")
		if _, statErr := os.Stat(defaultKC); statErr == nil {
			cfg, err = clientcmd.BuildConfigFromFlags("", defaultKC)
		} else {
			cfg, err = rest.InClusterConfig()
		}
	} else {
		cfg, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("building k8s config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating k8s clientset: %w", err)
	}

	return &K8sExecutor{
		clientset:  clientset,
		restConfig: cfg,
		namespace:  namespace,
	}, nil
}

// CopyScripts builds a tar archive from the scripts FS and pipes it into the pod.
func (e *K8sExecutor) CopyScripts(ctx context.Context, pod string, scripts fs.FS) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	err := fs.WalkDir(scripts, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == "." {
				return nil
			}
			return tw.WriteHeader(&tar.Header{
				Name:     filepath.Join("ta", path) + "/",
				Typeflag: tar.TypeDir,
				Mode:     0755,
			})
		}

		data, err := fs.ReadFile(scripts, path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		if err := tw.WriteHeader(&tar.Header{
			Name:     filepath.Join("ta", path),
			Size:     int64(len(data)),
			Mode:     0755,
			Typeflag: tar.TypeReg,
		}); err != nil {
			return fmt.Errorf("tar header for %s: %w", path, err)
		}

		if _, err := tw.Write(data); err != nil {
			return fmt.Errorf("tar write for %s: %w", path, err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("building tar: %w", err)
	}

	// Add a sudo shim so scripts calling sudo don't fail in K8s pods
	sudoShim := []byte("#!/bin/sh\nexec \"$@\"\n")
	if err := tw.WriteHeader(&tar.Header{
		Name:     "ta/sudo",
		Size:     int64(len(sudoShim)),
		Mode:     0755,
		Typeflag: tar.TypeReg,
	}); err != nil {
		return fmt.Errorf("tar header for sudo shim: %w", err)
	}
	if _, err := tw.Write(sudoShim); err != nil {
		return fmt.Errorf("tar write for sudo shim: %w", err)
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("closing tar: %w", err)
	}

	// Exec tar extraction on the pod
	req := e.clientset.CoreV1().RESTClient().Post().
		Resource("pods").SubResource("exec").
		Namespace(e.namespace).Name(pod).
		VersionedParams(&corev1.PodExecOptions{
			Command: []string{"tar", "xf", "-", "-C", "/tmp"},
			Stdin:   true,
			Stdout:  true,
			Stderr:  true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(e.restConfig, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("creating SPDY executor for copy: %w", err)
	}

	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  &buf,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return fmt.Errorf("tar extract on pod %s: %w (stderr: %s)", pod, err, stderr.String())
	}

	return nil
}

// Exec runs a single script on the pod and returns the result.
func (e *K8sExecutor) Exec(ctx context.Context, pod string, scriptPath string, useDzdo bool) (ExecResult, error) {
	remotePath := filepath.Join(remoteScriptDir, scriptPath)

	// Prepend /tmp/ta to PATH so our sudo shim is found before any missing sudo
	cmd := []string{"bash", "-c", fmt.Sprintf("export PATH=/tmp/ta:$PATH && chmod +x %s && %s", remotePath, remotePath)}

	req := e.clientset.CoreV1().RESTClient().Post().
		Resource("pods").SubResource("exec").
		Namespace(e.namespace).Name(pod).
		VersionedParams(&corev1.PodExecOptions{
			Command: cmd,
			Stdin:   true,
			Stdout:  true,
			Stderr:  true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(e.restConfig, "POST", req.URL())
	if err != nil {
		return ExecResult{}, fmt.Errorf("creating SPDY executor: %w", err)
	}

	execCtx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(execCtx, remotecommand.StreamOptions{
		Stdin:  bytes.NewReader(nil),
		Stdout: &stdout,
		Stderr: &stderr,
	})

	exitCode := 0
	if err != nil {
		if codeErr, ok := err.(interface{ ExitStatus() int }); ok {
			exitCode = codeErr.ExitStatus()
		} else {
			return ExecResult{
				Stdout:   stdout.String(),
				Stderr:   stderr.String(),
				ExitCode: 1,
				Status:   StatusFail,
			}, fmt.Errorf("exec on pod %s: %w", pod, err)
		}
	}

	return ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
		Status:   StatusFromExitCode(exitCode),
	}, nil
}

// Cleanup removes the deployed scripts from the pod.
func (e *K8sExecutor) Cleanup(ctx context.Context, pod string) error {
	req := e.clientset.CoreV1().RESTClient().Post().
		Resource("pods").SubResource("exec").
		Namespace(e.namespace).Name(pod).
		VersionedParams(&corev1.PodExecOptions{
			Command: []string{"rm", "-rf", remoteScriptDir},
			Stdin:   true,
			Stdout:  true,
			Stderr:  true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(e.restConfig, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("creating SPDY executor for cleanup: %w", err)
	}

	execCtx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	return exec.StreamWithContext(execCtx, remotecommand.StreamOptions{
		Stdin:  bytes.NewReader(nil),
		Stdout: &stdout,
		Stderr: &stderr,
	})
}

// FetchDiagnostics collects /tmp/diagnostics* from the pod.
func (e *K8sExecutor) FetchDiagnostics(ctx context.Context, pod string, localDir string) error {
	// Find latest diagnostics folder
	findResult, err := e.execSimple(ctx, pod, []string{
		"bash", "-c", "ls -1dt /tmp/diagnostics* 2>/dev/null | head -n 1",
	})
	if err != nil || strings.TrimSpace(findResult) == "" {
		return nil // no diagnostics to fetch
	}

	diagDir := strings.TrimSpace(findResult)

	// Create tar of diagnostics on pod and stream it back
	req := e.clientset.CoreV1().RESTClient().Post().
		Resource("pods").SubResource("exec").
		Namespace(e.namespace).Name(pod).
		VersionedParams(&corev1.PodExecOptions{
			Command: []string{"tar", "cf", "-", "-C", filepath.Dir(diagDir), filepath.Base(diagDir)},
			Stdin:   true,
			Stdout:  true,
			Stderr:  true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(e.restConfig, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("creating SPDY executor for diagnostics: %w", err)
	}

	podDiagDir := filepath.Join(localDir, fmt.Sprintf("%s_diagnostics", pod))
	if err := os.MkdirAll(podDiagDir, 0755); err != nil {
		return fmt.Errorf("creating diagnostics dir: %w", err)
	}

	execCtx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	var tarBuf, stderr bytes.Buffer
	err = exec.StreamWithContext(execCtx, remotecommand.StreamOptions{
		Stdin:  bytes.NewReader(nil),
		Stdout: &tarBuf,
		Stderr: &stderr,
	})
	if err != nil {
		return fmt.Errorf("streaming diagnostics tar from pod %s: %w (stderr: %s)", pod, err, stderr.String())
	}

	// Extract tar locally
	tr := tar.NewReader(&tarBuf)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		destPath := filepath.Join(podDiagDir, filepath.Base(hdr.Name))
		f, err := os.Create(destPath)
		if err != nil {
			continue
		}
		io.Copy(f, tr)
		f.Close()
	}

	// Remove remote diagnostics
	e.execSimple(ctx, pod, []string{"rm", "-rf", diagDir})

	return nil
}

// Close is a no-op for K8s (HTTP connections are per-request).
func (e *K8sExecutor) Close() error {
	return nil
}

func (e *K8sExecutor) execSimple(ctx context.Context, pod string, cmd []string) (string, error) {
	req := e.clientset.CoreV1().RESTClient().Post().
		Resource("pods").SubResource("exec").
		Namespace(e.namespace).Name(pod).
		VersionedParams(&corev1.PodExecOptions{
			Command: cmd,
			Stdin:   true,
			Stdout:  true,
			Stderr:  true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(e.restConfig, "POST", req.URL())
	if err != nil {
		return "", err
	}

	execCtx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(execCtx, remotecommand.StreamOptions{
		Stdin:  bytes.NewReader(nil),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	return stdout.String(), err
}

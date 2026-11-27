package main

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/execution"
)

// A Job's command line is written in internal/execution and read by this command, so a flag renamed
// on either side would stop every worker at its first argument. The Job's arguments parse as the
// worker's own, naming the directories its volumes are mounted at.
func TestAWorkerJobsCommandLineIsTheWorkersOwn(t *testing.T) {
	client := fake.NewSimpleClientset()
	k := &execution.Kubernetes{Client: client, Namespace: "abom", Image: "abom:dev", Pool: "discovery",
		Deadline: time.Hour, Args: []string{"--threads", "2", "--ram", "3072"}}
	ctx := context.Background()
	if err := k.Start(ctx, execution.Launch{Attempt: "0123456789abcdef01", Grant: authz.Envelope{PayloadType: authz.PayloadType}}); err != nil {
		t.Fatal(err)
	}
	jobs, err := client.BatchV1().Jobs("abom").List(ctx, metav1.ListOptions{})
	if err != nil || len(jobs.Items) != 1 {
		t.Fatalf("jobs = %d, %v", len(jobs.Items), err)
	}
	pod := jobs.Items[0].Spec.Template.Spec
	if len(pod.InitContainers) == 0 || len(pod.Containers) == 0 {
		t.Fatalf("the Job runs %d sidecars and %d containers", len(pod.InitContainers), len(pod.Containers))
	}
	mounted := func(c corev1.Container) map[string]string {
		out := map[string]string{}
		for _, m := range c.VolumeMounts {
			out[m.Name] = m.MountPath
		}
		return out
	}
	worker, attestor := pod.Containers[0], pod.InitContainers[0]
	if len(worker.Args) == 0 || worker.Args[0] != "worker" || len(attestor.Args) == 0 || attestor.Args[0] != "attestor" {
		t.Fatalf("the Job runs %v beside %v; want the worker and its attestor", worker.Args, attestor.Args)
	}
	c, err := parseWorkerArgs(worker.Args[1:], io.Discard)
	if err != nil {
		t.Fatalf("the worker cannot read the Job's arguments %v: %v", worker.Args[1:], err)
	}
	if wm := mounted(worker); c.attestor != wm["attest"]+"/attestor.sock" || c.workDir != wm["scratch"] || c.threads != 2 || c.ram != 3072 {
		t.Errorf("the worker reads %+v from a container that mounts %v", c, wm)
	}
	a, err := parseAttestorArgs(attestor.Args[1:], io.Discard)
	if err != nil {
		t.Fatalf("the attestor cannot read the Job's arguments %v: %v", attestor.Args[1:], err)
	}
	if am := mounted(attestor); a.grant != am["grant"]+"/grant.json" || a.trust != am["trust"] || a.svid != am["spiffe"] ||
		a.socket != c.attestor {
		t.Errorf("the attestor reads %+v from a container that mounts %v", a, am)
	}
}

// An attempt's key reaches its attestor in the environment, which every process it starts would
// inherit; it is read once and is gone after.
func TestAnAttestorTakesItsAttemptsKeyOutOfItsEnvironment(t *testing.T) {
	key, private, err := execution.NewAttemptKey()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(execution.AttemptKeySecret, private)
	got, err := takeAttemptKey()
	if err != nil || got.PublicPEM() != key.PublicPEM() {
		t.Fatalf("the attestor took %v, %v", got, err)
	}
	if left, ok := os.LookupEnv(execution.AttemptKeySecret); ok {
		t.Errorf("the attempt's key is still in the environment: %d bytes", len(left))
	}
}

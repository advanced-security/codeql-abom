package main

import (
	"context"
	"io"
	"testing"
	"time"

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
	args := pod.Containers[0].Args
	if len(args) == 0 || args[0] != "worker" {
		t.Fatalf("the Job runs %v; want the worker command", args)
	}
	c, err := parseWorkerArgs(args[1:], io.Discard)
	if err != nil {
		t.Fatalf("the worker cannot read the Job's arguments %v: %v", args[1:], err)
	}
	mounted := map[string]string{}
	for _, m := range pod.Containers[0].VolumeMounts {
		mounted[m.Name] = m.MountPath
	}
	if c.grant != mounted["grant"]+"/grant.json" || c.trust != mounted["trust"] || c.svid != mounted["spiffe"] ||
		c.workDir != mounted["scratch"] || c.threads != 2 || c.ram != 3072 {
		t.Errorf("the worker reads %+v from a pod that mounts %v", c, mounted)
	}
}

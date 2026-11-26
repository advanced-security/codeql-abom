package execution

import (
	"context"
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/advanced-security/codeql-abom/internal/authz"
)

// An attempt is one Job running the pool's ServiceAccount with its SPIFFE identity, holding no API
// token, and its grant and lent token in a Secret the Job owns; a failed Job says what the worker said.
// It runs at the pool's priority, below the coordinator and the database it must never displace.
func TestAnAttemptIsAJobOfThePoolsIdentityThatOwnsItsSecret(t *testing.T) {
	client := fake.NewSimpleClientset()
	k := &Kubernetes{Client: client, Namespace: "abom", Image: "abom:dev", Pool: "discovery",
		ServiceAccount: "abom-worker-discovery", TrustConfigMap: "abom-decision-trust", Deadline: time.Hour, Poll: time.Millisecond,
		PriorityClass: "abom-worker"}
	ctx := context.Background()
	attempt := "0123456789abcdef01234567"
	if err := k.Start(ctx, Launch{Attempt: attempt, Grant: authz.Envelope{PayloadType: authz.PayloadType},
		Secrets: map[string]string{"GITHUB_TOKEN": "t0k"}}); err != nil {
		t.Fatal(err)
	}

	name := k.name(attempt)
	job, err := client.BatchV1().Jobs("abom").Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pod := job.Spec.Template.Spec
	if pod.PriorityClassName != "abom-worker" {
		t.Errorf("pod runs at priority class %q; want the pool's", pod.PriorityClassName)
	}
	if pod.ServiceAccountName != "abom-worker-discovery" || pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Errorf("pod runs %q with token automount %v", pod.ServiceAccountName, pod.AutomountServiceAccountToken)
	}
	var csi bool
	for _, v := range pod.Volumes {
		csi = csi || (v.CSI != nil && v.CSI.Driver == "spiffe.csi.cert-manager.io")
	}
	if !csi || *pod.SecurityContext.RunAsNonRoot != true {
		t.Errorf("the pod has no SPIFFE volume (%v) or runs as root", csi)
	}
	secret, err := client.CoreV1().Secrets("abom").Get(ctx, name, metav1.GetOptions{})
	if err != nil || len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].Kind != "Job" || string(secret.Data["GITHUB_TOKEN"]) != "t0k" {
		t.Errorf("secret = %+v, %v", secret, err)
	}
	for _, e := range pod.Containers[0].Env {
		if e.Name == "GITHUB_TOKEN" && e.Value != "" {
			t.Error("the token is in the Job spec rather than referenced from its Secret")
		}
	}

	job.Status.Succeeded = 1
	if _, err := client.BatchV1().Jobs("abom").UpdateStatus(ctx, job, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := k.Wait(ctx, attempt); err != nil {
		t.Errorf("a succeeded job: %v", err)
	}

	job.Status.Succeeded, job.Status.Failed = 0, 1
	if _, err := client.BatchV1().Jobs("abom").UpdateStatus(ctx, job, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().Pods("abom").Create(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name + "-x", Namespace: "abom",
		Labels: map[string]string{"abom.github.com/attempt": attempt}}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "Error", Message: "worker: the grant has expired"}}}}}},
		metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := k.Wait(ctx, attempt); err == nil || !strings.Contains(err.Error(), "the grant has expired") {
		t.Errorf("a failed job: %v", err)
	}
}

// Two releases can share a namespace, and a coordinator starting reaps its own release's leftover
// Jobs, never the other's, whose coordinator is still collecting them.
func TestACoordinatorReapsOnlyItsOwnReleasesJobs(t *testing.T) {
	client := fake.NewSimpleClientset()
	ctx := context.Background()
	ours := &Kubernetes{Client: client, Namespace: "shared", Pool: "discovery", Instance: "abom"}
	theirs := &Kubernetes{Client: client, Namespace: "shared", Pool: "discovery", Instance: "abom-staging"}
	for _, l := range []struct {
		k       *Kubernetes
		attempt string
	}{{ours, "aaaaaaaaaaaaaaaa01"}, {theirs, "bbbbbbbbbbbbbbbb02"}} {
		if err := l.k.Start(ctx, Launch{Attempt: l.attempt, Grant: authz.Envelope{PayloadType: authz.PayloadType}}); err != nil {
			t.Fatal(err)
		}
	}

	if reaped, err := ours.Reap(ctx); err != nil || reaped != 1 {
		t.Fatalf("reaped %d, %v; want this release's one Job", reaped, err)
	}
	jobs, err := client.BatchV1().Jobs("shared").List(ctx, metav1.ListOptions{})
	if err != nil || len(jobs.Items) != 1 || jobs.Items[0].Labels["app.kubernetes.io/instance"] != "abom-staging" {
		t.Errorf("left %+v, %v; want the other release's Job alone", jobs.Items, err)
	}
}

// A coordinator that stopped leaves its Jobs running with nobody to collect their results, and the
// next one releases their claims; it reaps its own pool's Jobs and nobody else's.
func TestAStartingCoordinatorReapsOnlyItsPoolsLeftoverJobs(t *testing.T) {
	client := fake.NewSimpleClientset()
	ctx := context.Background()
	discovery := &Kubernetes{Client: client, Namespace: "abom", Pool: "discovery"}
	qlaas := &Kubernetes{Client: client, Namespace: "abom", Pool: "qlaas-test"}
	for _, l := range []struct {
		k       *Kubernetes
		attempt string
	}{{discovery, "aaaaaaaaaaaaaaaa01"}, {discovery, "bbbbbbbbbbbbbbbb02"}, {qlaas, "cccccccccccccccc03"}} {
		if err := l.k.Start(ctx, Launch{Attempt: l.attempt, Grant: authz.Envelope{PayloadType: authz.PayloadType}}); err != nil {
			t.Fatal(err)
		}
	}
	unrelated := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "nightly-backup", Namespace: "abom"}}
	if _, err := client.BatchV1().Jobs("abom").Create(ctx, unrelated, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	reaped, err := discovery.Reap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reaped != 2 {
		t.Errorf("reaped %d jobs; want the discovery pool's 2", reaped)
	}
	jobs, err := client.BatchV1().Jobs("abom").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, j := range jobs.Items {
		left = append(left, j.Name)
	}
	slices.Sort(left)
	if want := []string{"abom-qlaas-test-cccccccccccccccc", "nightly-backup"}; !slices.Equal(left, want) {
		t.Errorf("left %v; want %v", left, want)
	}
}

// The Secret holding a grant and a lent token is owned by its Job from the moment it exists, so
// nothing a coordinator does or fails to do after creating it can leave it behind: the Job is made
// suspended, the Secret is made owned by it, and only then does the Job start.
func TestAnAttemptsSecretIsOwnedByItsJobFromTheMomentItExists(t *testing.T) {
	client := fake.NewSimpleClientset()
	k := &Kubernetes{Client: client, Namespace: "abom", Image: "abom:dev", Pool: "discovery", Deadline: time.Hour}
	ctx := context.Background()
	if err := k.Start(ctx, Launch{Attempt: "0123456789abcdef01", Grant: authz.Envelope{PayloadType: authz.PayloadType},
		Secrets: map[string]string{"GITHUB_TOKEN": "t0k"}}); err != nil {
		t.Fatal(err)
	}
	var steps []string
	for _, a := range client.Actions() {
		steps = append(steps, a.GetVerb()+" "+a.GetResource().Resource)
		switch create, ok := a.(k8stesting.CreateAction); {
		case ok && a.GetVerb() == "create":
			switch obj := create.GetObject().(type) {
			case *batchv1.Job:
				if obj.Spec.Suspend == nil || !*obj.Spec.Suspend {
					t.Error("the Job was created running, before its Secret existed")
				}
			case *corev1.Secret:
				if len(obj.OwnerReferences) != 1 || obj.OwnerReferences[0].Kind != "Job" {
					t.Errorf("the Secret was created owned by %+v; want its Job", obj.OwnerReferences)
				}
			}
		case a.GetVerb() == "update" && a.GetResource().Resource == "secrets":
			t.Error("the Secret was updated after it was made, so for a moment nothing owned it")
		}
	}
	if want := []string{"create jobs", "create secrets", "patch jobs"}; !slices.Equal(steps, want) {
		t.Errorf("starting an attempt did %v; want %v", steps, want)
	}
	job, err := client.BatchV1().Jobs("abom").Get(ctx, k.name("0123456789abcdef01"), metav1.GetOptions{})
	if err != nil || (job.Spec.Suspend != nil && *job.Spec.Suspend) {
		t.Errorf("after starting, the Job is %+v, %v; want it resumed", job.Spec.Suspend, err)
	}
}

// A Secret that cannot be made leaves no Job waiting for it.
func TestAnAttemptWhoseSecretCannotBeMadeLeavesNoJob(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("quota exceeded")
	})
	k := &Kubernetes{Client: client, Namespace: "abom", Pool: "discovery", Deadline: time.Hour}
	ctx := context.Background()
	if err := k.Start(ctx, Launch{Attempt: "0123456789abcdef01", Grant: authz.Envelope{PayloadType: authz.PayloadType}}); err == nil {
		t.Fatal("an attempt without its Secret started")
	}
	if jobs, _ := client.BatchV1().Jobs("abom").List(ctx, metav1.ListOptions{}); len(jobs.Items) != 0 {
		t.Errorf("%d Jobs were left waiting for a Secret that was never made", len(jobs.Items))
	}
}

// Cleaning up an attempt deletes its Secret whether or not deleting the Job also would: the token
// it holds must not outlive the attempt by however long garbage collection takes.
func TestCleaningUpAnAttemptDeletesItsSecret(t *testing.T) {
	client := fake.NewSimpleClientset()
	k := &Kubernetes{Client: client, Namespace: "abom", Pool: "discovery", Deadline: time.Hour}
	ctx := context.Background()
	attempt := "0123456789abcdef01"
	if err := k.Start(ctx, Launch{Attempt: attempt, Grant: authz.Envelope{PayloadType: authz.PayloadType},
		Secrets: map[string]string{"GITHUB_TOKEN": "t0k"}}); err != nil {
		t.Fatal(err)
	}
	k.Cleanup(ctx, attempt)
	if _, err := client.CoreV1().Secrets("abom").Get(ctx, k.name(attempt), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("the attempt's Secret is still there after cleanup: %v", err)
	}
}

// A variable the object store reads and its Jobs are not given would leave every worker unable to
// reach what its coordinator can: S3Env is what is forwarded, so it names every one read.
func TestEveryObjectStoreVariableIsOneAWorkerIsGiven(t *testing.T) {
	src, err := os.ReadFile("s3.go")
	if err != nil {
		t.Fatal(err)
	}
	read := regexp.MustCompile(`os\.Getenv\("(ABOM_S3_\w+)"\)`).FindAllStringSubmatch(string(src), -1)
	if len(read) == 0 {
		t.Fatal("s3.go reads no ABOM_S3_ variable; this test no longer sees how the store is configured")
	}
	for _, m := range read {
		if !slices.Contains(S3Env, m[1]) {
			t.Errorf("s3.go reads %s, which S3Env does not forward to workers", m[1])
		}
	}
}

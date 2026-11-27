package execution

import (
	"context"
	"errors"
	"maps"
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

// container is the one of pod named name, among its containers and its sidecars.
func container(t *testing.T, pod corev1.PodSpec, name string) corev1.Container {
	t.Helper()
	for _, c := range append(slices.Clone(pod.InitContainers), pod.Containers...) {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("the pod runs no %s container", name)
	return corev1.Container{}
}

// A worker reads a repository, or what an agent left, and the CLI follows a link in what it reads, so
// every pool's worker holds none of the attempt's secrets: an attestor beside it holds the grant, the
// decision bundle, the SPIFFE identity, the attempt's key and the object store, and the two share only
// a socket's directory. The attestor is a sidecar, so it outlives the worker's grace period.
func TestAnAttestorBesideEveryWorkerHoldsItsSecrets(t *testing.T) {
	client := fake.NewSimpleClientset()
	k := &Kubernetes{Client: client, Namespace: "abom", Image: "abom:dev", Pool: "discovery",
		ServiceAccount: "abom-worker-discovery", TrustConfigMap: "abom-decision-trust", Deadline: time.Hour,
		Env:     []corev1.EnvVar{{Name: "ABOM_S3_BUCKET", Value: "abom"}},
		EnvFrom: []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "abom-s3"}}}}}
	ctx := context.Background()
	attempt := "0123456789abcdef01234567"
	if err := k.Start(ctx, Launch{Attempt: attempt, Grant: authz.Envelope{PayloadType: authz.PayloadType},
		Secrets: map[string]string{AttemptKeySecret: "-----BEGIN PRIVATE KEY-----", TokenSecret: "t0k"}}); err != nil {
		t.Fatal(err)
	}
	job, err := client.BatchV1().Jobs("abom").Get(ctx, k.name(attempt), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pod := job.Spec.Template.Spec
	attestor, worker := container(t, pod, "attestor"), container(t, pod, "worker")
	if attestor.RestartPolicy == nil || *attestor.RestartPolicy != corev1.ContainerRestartPolicyAlways || pod.InitContainers[0].Name != "attestor" {
		t.Error("the attestor is not a sidecar started before the worker")
	}
	mounts := func(c corev1.Container) map[string]string {
		out := map[string]string{}
		for _, m := range c.VolumeMounts {
			out[m.Name] = m.MountPath
		}
		return out
	}
	env := func(c corev1.Container) map[string]bool {
		out := map[string]bool{}
		for _, e := range c.Env {
			out[e.Name] = true
		}
		return out
	}
	am, wm := mounts(attestor), mounts(worker)
	for _, v := range []string{"grant", "trust", "spiffe"} {
		if am[v] == "" || wm[v] != "" {
			t.Errorf("%s is mounted by the attestor at %q and by the worker at %q", v, am[v], wm[v])
		}
	}
	if am["attest"] == "" || am["attest"] != wm["attest"] || am["scratch"] != "" {
		t.Errorf("the attestor and worker share %q and %q, and the attestor reads the scratch at %q",
			am["attest"], wm["attest"], am["scratch"])
	}
	ae, we := env(attestor), env(worker)
	if !ae[AttemptKeySecret] || !ae["ABOM_S3_BUCKET"] || len(attestor.EnvFrom) != 1 {
		t.Errorf("the attestor holds %v and %d sources; want the attempt's key and the object store", ae, len(attestor.EnvFrom))
	}
	if we[AttemptKeySecret] || we["ABOM_S3_BUCKET"] || len(worker.EnvFrom) != 0 || !we[TokenSecret] || ae[TokenSecret] {
		t.Errorf("the worker holds %v and %d sources; want the token it reads with and nothing of the attempt's", we, len(worker.EnvFrom))
	}
	if job.Spec.Template.Annotations["eks.amazonaws.com/skip-containers"] != "worker,agent" {
		t.Errorf("a cloud role annotated on the pool's ServiceAccount would reach the worker: %v", job.Spec.Template.Annotations)
	}
	if attestor.SecurityContext == nil || attestor.SecurityContext.ReadOnlyRootFilesystem == nil ||
		!*attestor.SecurityContext.ReadOnlyRootFilesystem {
		t.Error("the attestor may write its root filesystem")
	}
}

// Every pool reaches out through its own listener of the egress proxy, which admits only the hosts that
// pool needs: its worker and its attestor are told to, in both spellings tools read, and reach directly
// only the pod's loopback and what the cluster serves inside it.
func TestEveryWorkerReachesOutThroughItsPoolsProxy(t *testing.T) {
	k := &Kubernetes{Client: fake.NewSimpleClientset(), Namespace: "abom", Image: "abom:dev", Pool: "discovery",
		Deadline: time.Hour, Proxy: "http://abom-egress-proxy:3129", NoProxy: []string{"minio.abom.svc"}}
	pod := k.job("abom-discovery-0123", map[string]string{}, nil, false).Spec.Template.Spec
	for _, name := range []string{"worker", "attestor"} {
		env := map[string]string{}
		for _, e := range container(t, pod, name).Env {
			env[e.Name] = e.Value
		}
		for _, v := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
			if env[v] != k.Proxy {
				t.Errorf("the %s's %s is %q; want its pool's proxy", name, v, env[v])
			}
		}
		for _, v := range []string{"NO_PROXY", "no_proxy"} {
			if env[v] != "127.0.0.1,localhost,minio.abom.svc" {
				t.Errorf("the %s's %s is %q", name, v, env[v])
			}
		}
	}
	k.Proxy, k.NoProxy = "", nil
	pod = k.job("abom-discovery-0123", map[string]string{}, nil, false).Spec.Template.Spec
	for _, e := range container(t, pod, "worker").Env {
		if strings.Contains(strings.ToUpper(e.Name), "PROXY") {
			t.Errorf("a pool given no proxy names %s=%q", e.Name, e.Value)
		}
	}
}

// A worker writes only where a volume is mounted: its scratch directory, and /tmp, where CodeQL's JVM
// keeps its own files. Everything else, HOME and the packs the image installed beneath it included,
// is read-only as the coordinator's is, so no attempt changes what the next one in its pod runs.
func TestAWorkerWritesOnlyToItsVolumes(t *testing.T) {
	k := &Kubernetes{Client: fake.NewSimpleClientset(), Namespace: "abom", Image: "abom:dev", Pool: "discovery",
		Deadline: time.Hour}
	pod := k.job("abom-discovery-0123", map[string]string{}, nil, false).Spec.Template.Spec
	c := pod.Containers[0]
	if c.SecurityContext.ReadOnlyRootFilesystem == nil || !*c.SecurityContext.ReadOnlyRootFilesystem {
		t.Error("a worker's root filesystem is writable")
	}
	volumes := map[string]corev1.Volume{}
	for _, v := range pod.Volumes {
		volumes[v.Name] = v
	}
	writable := map[string]bool{}
	for _, m := range c.VolumeMounts {
		if v, ok := volumes[m.Name]; ok && v.EmptyDir != nil && !m.ReadOnly {
			writable[m.MountPath] = true
		}
	}
	for _, path := range []string{podWork, "/tmp"} {
		if !writable[path] {
			t.Errorf("%s is not a writable volume of the attempt's own", path)
		}
	}
	if !slices.Contains(c.Args, podWork) {
		t.Errorf("the worker is not told to work in %s: %v", podWork, c.Args)
	}
	// The lock every CodeQL invocation takes is made in the user's cache directory, which would
	// otherwise be beneath the read-only HOME; every analysis failed on kind before it began.
	var cache string
	for _, e := range c.Env {
		if e.Name == "XDG_CACHE_HOME" {
			cache = e.Value
		}
	}
	if !slices.ContainsFunc(slices.Collect(maps.Keys(writable)), func(m string) bool {
		return cache != "" && (cache == m || strings.HasPrefix(cache, m+"/"))
	}) {
		t.Errorf("the worker's cache directory %q is not on a writable volume", cache)
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
	// A leftover Job's Secret holds a token as live as a running one's, so it goes by name with the
	// Job rather than waiting on garbage collection.
	secrets, err := client.CoreV1().Secrets("abom").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, s := range secrets.Items {
		kept = append(kept, s.Name)
	}
	if want := []string{"abom-qlaas-test-cccccccccccccccc"}; !slices.Equal(kept, want) {
		t.Errorf("left Secrets %v; want only the other pool's %v", kept, want)
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

// Stopping an attempt suspends its Job, which sends its pod SIGTERM and leaves the worker its grace
// period to report and sign what it did; waiting on it answers ErrStopped only once no pod is left, so
// what the worker wrote while stopping is read, and not before.
func TestStoppingAnAttemptSuspendsItsJobAndWaitsForItsPodToGo(t *testing.T) {
	client := fake.NewSimpleClientset()
	k := &Kubernetes{Client: client, Namespace: "abom", Pool: "authoring", Deadline: time.Hour, Poll: time.Millisecond}
	ctx := context.Background()
	attempt := "0123456789abcdef01"
	if err := k.Start(ctx, Launch{Attempt: attempt, Grant: authz.Envelope{PayloadType: authz.PayloadType}}); err != nil {
		t.Fatal(err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: k.name(attempt) + "-x", Namespace: "abom",
		Labels: map[string]string{"abom.github.com/attempt": attempt}}}
	if _, err := client.CoreV1().Pods("abom").Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := k.Stop(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	job, err := client.BatchV1().Jobs("abom").Get(ctx, k.name(attempt), metav1.GetOptions{})
	if err != nil || job.Spec.Suspend == nil || !*job.Spec.Suspend {
		t.Fatalf("after stopping, the Job is %+v, %v; want it suspended", job.Spec.Suspend, err)
	}
	waiting, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := k.Wait(waiting, attempt); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("waiting while the stopped pod is still finishing answered %v", err)
	}
	if err := client.CoreV1().Pods("abom").Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := k.Wait(ctx, attempt); !errors.Is(err, ErrStopped) {
		t.Errorf("waiting once the stopped pod is gone answered %v; want ErrStopped", err)
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

// sessionPod is the pod an authoring session's Job runs, sealed or not: its spec, the agent, and the
// worker.
func sessionPod(t *testing.T, sealed bool) (corev1.PodSpec, corev1.Container, corev1.Container) {
	t.Helper()
	client := fake.NewSimpleClientset()
	k := &Kubernetes{Client: client, Namespace: "abom", Image: "abom:dev", Pool: "authoring",
		ServiceAccount: "abom-worker-authoring", TrustConfigMap: "abom-decision-trust", Deadline: time.Hour,
		Env: []corev1.EnvVar{{Name: "ABOM_S3_BUCKET", Value: "abom"}},
		EnvFrom: []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: "abom-s3"}}}},
		Agent: &Agent{Workspace: "/var/lib/abom/custom-queries", Command: []string{"copilot"},
			Proxy: "http://abom-egress:3128"}}
	ctx := context.Background()
	attempt := "0123456789abcdef01234567"
	if err := k.Start(ctx, Launch{Attempt: attempt, Grant: authz.Envelope{PayloadType: authz.PayloadType},
		Secrets: map[string]string{CopilotTokenSecret: "ghs_x", AgentConnectionSecret: "c0nn"}, Sealed: sealed}); err != nil {
		t.Fatal(err)
	}
	job, err := client.BatchV1().Jobs("abom").Get(ctx, k.name(attempt), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pod := job.Spec.Template.Spec
	if len(pod.InitContainers) != 2 || len(pod.Containers) != 1 {
		t.Fatalf("the pod runs %d init containers and %d containers", len(pod.InitContainers), len(pod.Containers))
	}
	return pod, container(t, pod, "agent"), container(t, pod, "worker")
}

// A session's pod runs the Copilot CLI as a sidecar the worker waits on, unable to write its own
// root or keep a capability, and gives the worker time to report before the pod is killed.
func TestASessionsPodRunsItsAgentAsASidecarTheWorkerWaitsOn(t *testing.T) {
	pod, agent, _ := sessionPod(t, false)
	if agent.RestartPolicy == nil || *agent.RestartPolicy != corev1.ContainerRestartPolicyAlways ||
		agent.StartupProbe == nil || agent.StartupProbe.TCPSocket == nil ||
		agent.StartupProbe.TCPSocket.Port.IntValue() != AgentPort {
		t.Errorf("the agent is not a sidecar the worker waits on: %+v", agent)
	}
	if agent.SecurityContext == nil || agent.SecurityContext.ReadOnlyRootFilesystem == nil ||
		!*agent.SecurityContext.ReadOnlyRootFilesystem || len(agent.SecurityContext.Capabilities.Drop) == 0 {
		t.Error("the agent may write its root filesystem or keeps capabilities")
	}
	if pod.TerminationGracePeriodSeconds == nil || *pod.TerminationGracePeriodSeconds < 30 {
		t.Error("the worker is given no time to report before its pod is killed")
	}
}

// Only the agent holds the session's Copilot token, and it holds nothing of the object store; both
// read the token the agent admits the worker with from the Job's Secret, and the agent reaches out
// only through the egress proxy.
func TestOnlyASessionsAgentHoldsItsCopilotToken(t *testing.T) {
	_, agent, worker := sessionPod(t, false)
	fromSecret := func(c corev1.Container, name string) bool {
		for _, e := range c.Env {
			if e.Name == name {
				return e.Value == "" && e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil &&
					e.ValueFrom.SecretKeyRef.Key == name
			}
		}
		return false
	}
	if !fromSecret(agent, CopilotTokenSecret) || !fromSecret(agent, AgentConnectionSecret) ||
		!fromSecret(worker, AgentConnectionSecret) {
		t.Error("the tokens do not reach the containers that need them from the Secret")
	}
	for _, e := range worker.Env {
		if e.Name == CopilotTokenSecret {
			t.Error("the worker holds the session's Copilot token")
		}
	}
	if len(agent.EnvFrom) != 0 {
		t.Error("the agent holds the object store's credentials")
	}
	env := map[string]string{}
	for _, e := range agent.Env {
		env[e.Name] = e.Value
	}
	if _, ok := env["ABOM_S3_BUCKET"]; ok || env["HTTPS_PROXY"] != "http://abom-egress:3128" ||
		!strings.Contains(env["NO_PROXY"], "127.0.0.1") {
		t.Errorf("the agent's environment is %v", env)
	}
}

// The agent shares the pack and the scratch directory with the worker, at the same paths, and none of
// the grant, the decision bundle, the worker's identity or its /tmp.
func TestASessionsAgentSharesOnlyThePackAndScratch(t *testing.T) {
	_, agent, worker := sessionPod(t, false)
	mounts := func(c corev1.Container) map[string]corev1.VolumeMount {
		out := map[string]corev1.VolumeMount{}
		for _, m := range c.VolumeMounts {
			out[m.MountPath] = m
		}
		return out
	}
	am, wm := mounts(agent), mounts(worker)
	for _, path := range []string{podGrant, podTrust, podSVID, podAttest} {
		if _, ok := am[path]; ok {
			t.Errorf("the agent mounts %s", path)
		}
	}
	for _, path := range []string{"/var/lib/abom/custom-queries", podWork} {
		if am[path].Name == "" || am[path].Name != wm[path].Name || am[path].SubPath != wm[path].SubPath {
			t.Errorf("the agent and the worker do not share %s: %+v and %+v", path, am[path], wm[path])
		}
	}
	if am[podTemp].Name == "" || am[podTemp].Name == wm[podTemp].Name {
		t.Error("the agent and the worker share a /tmp")
	}
}

// A draft's agent is offered ABOM's tools and nothing else, so a sealed session's agent reads the pack
// and writes nowhere the worker reads: it can leave no link or pipe there for the worker to follow.
func TestASealedSessionsAgentWritesNothingTheWorkerReads(t *testing.T) {
	_, agent, worker := sessionPod(t, true)
	for _, m := range agent.VolumeMounts {
		if !m.ReadOnly && m.MountPath != podTemp {
			t.Errorf("a sealed session's agent writes %s", m.MountPath)
		}
	}
	shared := false
	for _, m := range agent.VolumeMounts {
		shared = shared || m.MountPath == "/var/lib/abom/custom-queries" && m.ReadOnly
	}
	for _, m := range worker.VolumeMounts {
		if m.MountPath == "/var/lib/abom/custom-queries" && m.ReadOnly {
			t.Error("a sealed session's worker cannot write the pack")
		}
	}
	if !shared {
		t.Errorf("a sealed session's agent does not read the pack: %+v", agent.VolumeMounts)
	}
}

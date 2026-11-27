package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

// Paths inside a worker pod.
const (
	podGrant = "/var/run/abom/grant"
	podTrust = "/etc/abom/trust"
	podSVID  = "/var/run/secrets/spiffe.io"
	podWork  = "/scratch"
	// podTemp is where CodeQL's JVM keeps its own files; nothing else beyond podWork is writable.
	podTemp = "/tmp"
	// podAttest holds the socket a worker reaches its attestor on, which only the two mount.
	podAttest = "/var/run/abom/attest"
)

// podAttestor is the socket a worker reaches its attestor on.
const podAttestor = podAttest + "/attestor.sock"

// Kubernetes launches each attempt as a Job in one namespace, running the pool's ServiceAccount
// with its SPIFFE identity mounted by csi-driver-spiffe. The grant, the attempt's key and any lent
// token travel in a Secret the Job owns from the moment the Secret exists, so all go when the Job does.
// Every pod runs an attestor beside its worker, which alone holds the grant, the decision bundle, the
// identity, the attempt's key and the object store; the worker holds only a token it reads with.
type Kubernetes struct {
	Client    kubernetes.Interface
	Namespace string
	Image     string
	Pool      string
	// Instance is the release the Jobs belong to, so two releases in one namespace each reap and
	// admit only their own; empty labels none.
	Instance       string
	ServiceAccount string
	// TrustConfigMap holds trust.json, the decision point's bundle the worker verifies its grant with.
	TrustConfigMap string
	// Env and EnvFrom carry the object store's configuration and credentials, to the attestor alone.
	Env       []corev1.EnvVar
	EnvFrom   []corev1.EnvFromSource
	Resources corev1.ResourceRequirements
	// AttestorResources are the attestor's; empty is a small share, with room to hold the largest
	// output a worker hands it.
	AttestorResources corev1.ResourceRequirements
	// PriorityClass ranks attempts below the coordinator and the database, so under pressure the
	// scheduler and the kubelet give way with analysis first.
	PriorityClass string
	Deadline      time.Duration
	// Args are added to the worker command, such as CodeQL's thread and RAM budget.
	Args []string
	Poll time.Duration
	// Agent, where set, runs the Copilot CLI beside the worker of every pod: a pool of sessions.
	Agent *Agent
	// Proxy is the pool's own listener of the egress proxy, as a URL, which the worker and the attestor
	// reach out through; NoProxy are the hosts beside the pod's loopback they reach directly, such as an
	// object store inside the cluster. Empty names none.
	Proxy   string
	NoProxy []string
}

// proxyEnv tells a container to reach out through the pool's proxy, in both spellings tools read.
func (k *Kubernetes) proxyEnv() []corev1.EnvVar {
	if k.Proxy == "" {
		return nil
	}
	direct := strings.Join(append([]string{"127.0.0.1", "localhost"}, k.NoProxy...), ",")
	var env []corev1.EnvVar
	for _, name := range []string{"HTTPS_PROXY", "HTTP_PROXY"} {
		env = append(env, corev1.EnvVar{Name: name, Value: k.Proxy}, corev1.EnvVar{Name: strings.ToLower(name), Value: k.Proxy})
	}
	return append(env, corev1.EnvVar{Name: "NO_PROXY", Value: direct}, corev1.EnvVar{Name: "no_proxy", Value: direct})
}

// AgentPort is where a session pod's Copilot CLI serves its worker, on the pod's loopback.
const AgentPort = 7777

// AgentURLVariable is the variable a session pod's worker reads where its agent serves from.
const AgentURLVariable = "ABOM_AGENT_URL"

// Agent is the Copilot CLI a session pod runs beside its worker. It holds the session's Copilot
// token and nothing the worker holds, and reaches out only through the egress proxy.
type Agent struct {
	// Workspace is the coordinator's pack, which both containers see at that path: every path the
	// worker hands the agent is one the coordinator lands from.
	Workspace string
	// Command starts the CLI; the flags that make it serve the worker follow it.
	Command []string
	// Image runs the CLI; empty is the worker's.
	Image string
	// Proxy is the egress proxy the agent reaches Copilot through, as a URL.
	Proxy     string
	Resources corev1.ResourceRequirements
}

func (k *Kubernetes) name(attempt string) string {
	return "abom-" + k.Pool + "-" + attempt[:min(len(attempt), 16)]
}

func (k *Kubernetes) Start(ctx context.Context, l Launch) error {
	grant, err := json.Marshal(l.Grant)
	if err != nil {
		return err
	}
	name := k.name(l.Attempt)
	labels := k.poolLabels()
	labels["abom.github.com/attempt"] = l.Attempt
	data := map[string][]byte{"grant.json": grant}
	for key, value := range l.Secrets {
		data[key] = []byte(value)
	}
	// Suspended until its Secret exists, so the Secret can be made already owned by the Job.
	job, err := k.Client.BatchV1().Jobs(k.Namespace).Create(ctx, k.job(name, labels, l.Secrets, l.Sealed), metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating the worker job: %w", err)
	}
	abandon := func() { k.Cleanup(context.WithoutCancel(ctx), l.Attempt) }
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: k.Namespace, Labels: labels,
		OwnerReferences: []metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: job.Name, UID: job.UID}}},
		Data: data}
	if _, err := k.Client.CoreV1().Secrets(k.Namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		abandon()
		return fmt.Errorf("creating the attempt's secret: %w", err)
	}
	resume := []byte(`{"spec":{"suspend":false}}`)
	if _, err := k.Client.BatchV1().Jobs(k.Namespace).Patch(ctx, name, types.MergePatchType, resume, metav1.PatchOptions{}); err != nil {
		abandon()
		return fmt.Errorf("starting the worker job: %w", err)
	}
	return nil
}

func (k *Kubernetes) job(name string, labels map[string]string, secrets map[string]string, sealed bool) *batchv1.Job {
	no, yes := false, true
	uid := int64(10001)
	deadline := int64(k.Deadline / time.Second)
	ttl := int32(600)
	backoff := int32(0)
	fromSecret := func(key string) corev1.EnvVar {
		return corev1.EnvVar{Name: key, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: key}}}
	}
	// The lock every CodeQL invocation takes is made in the user's cache directory, beneath the
	// read-only HOME unless it is moved.
	env := append([]corev1.EnvVar{{Name: "XDG_CACHE_HOME", Value: podTemp + "/cache"}}, k.proxyEnv()...)
	if _, ok := secrets[TokenSecret]; ok {
		env = append(env, fromSecret(TokenSecret))
	}
	args := append([]string{"worker", "--attestor", podAttestor, "--work-dir", podWork}, k.Args...)
	restricted := func() *corev1.SecurityContext {
		return &corev1.SecurityContext{AllowPrivilegeEscalation: &no, ReadOnlyRootFilesystem: &yes,
			Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}}
	}
	always := corev1.ContainerRestartPolicyAlways
	attestor := corev1.Container{
		Name: "attestor", Image: k.Image, RestartPolicy: &always,
		Args: []string{"attestor", "--grant", podGrant + "/grant.json", "--trust", podTrust, "--svid", podSVID,
			"--socket", podAttestor},
		Env: append(append(slices.Clone(k.Env), k.proxyEnv()...), fromSecret(AttemptKeySecret)), EnvFrom: k.EnvFrom,
		Resources:                k.attestorResources(),
		TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError, SecurityContext: restricted(),
		VolumeMounts: []corev1.VolumeMount{
			{Name: "grant", MountPath: podGrant, ReadOnly: true},
			{Name: "trust", MountPath: podTrust, ReadOnly: true},
			{Name: "spiffe", MountPath: podSVID, ReadOnly: true},
			{Name: "attest", MountPath: podAttest},
		},
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: k.Namespace, Labels: labels},
		Spec: batchv1.JobSpec{
			Suspend:      &yes,
			BackoffLimit: &backoff, ActiveDeadlineSeconds: &deadline, TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				// A cloud role on the pool's ServiceAccount is injected into every container but these.
				ObjectMeta: metav1.ObjectMeta{Labels: labels,
					Annotations: map[string]string{"eks.amazonaws.com/skip-containers": "worker,agent"}},
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					ServiceAccountName:           k.ServiceAccount,
					PriorityClassName:            k.PriorityClass,
					AutomountServiceAccountToken: &no,
					SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: &yes, RunAsUser: &uid, RunAsGroup: &uid,
						FSGroup: &uid, SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
					InitContainers: []corev1.Container{attestor},
					Containers: []corev1.Container{{
						Name: "worker", Image: k.Image, Args: args, Env: env, Resources: k.Resources,
						TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
						SecurityContext:          restricted(),
						VolumeMounts: []corev1.VolumeMount{
							{Name: "scratch", MountPath: podWork},
							{Name: "tmp", MountPath: podTemp},
							{Name: "attest", MountPath: podAttest},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "grant", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: name,
							Items: []corev1.KeyToPath{{Key: "grant.json", Path: "grant.json"}}}}},
						{Name: "trust", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: k.TrustConfigMap}}}},
						{Name: "spiffe", VolumeSource: corev1.VolumeSource{CSI: &corev1.CSIVolumeSource{
							Driver: "spiffe.csi.cert-manager.io", ReadOnly: &yes}}},
						{Name: "scratch", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{Name: "attest", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					},
				},
			},
		},
	}
	if k.Agent != nil {
		k.withAgent(&job.Spec.Template.Spec, name, sealed)
	}
	return job
}

// attestorResources are the attestor's: a small share unless told otherwise, with room for the largest
// output it is handed, a test database, held whole while it is written.
func (k *Kubernetes) attestorResources() corev1.ResourceRequirements {
	if len(k.AttestorResources.Requests) > 0 || len(k.AttestorResources.Limits) > 0 {
		return k.AttestorResources
	}
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m"), corev1.ResourceMemory: resource.MustParse("64Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("1Gi")},
	}
}

// withAgent runs the Copilot CLI beside the worker, as a native sidecar the worker starts after. A sealed
// session's agent reads the pack and writes nowhere the worker reads, which is everywhere it needs.
func (k *Kubernetes) withAgent(pod *corev1.PodSpec, secret string, sealed bool) {
	no, yes, always := false, true, corev1.ContainerRestartPolicyAlways
	grace := int64(60)
	fromSecret := func(key string) corev1.EnvVar {
		return corev1.EnvVar{Name: key, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secret}, Key: key}}}
	}
	pack := corev1.VolumeMount{Name: "scratch", MountPath: k.Agent.Workspace, SubPath: "pack"}
	worker := &pod.Containers[0]
	worker.Env = append(worker.Env, fromSecret(AgentConnectionSecret),
		corev1.EnvVar{Name: AgentURLVariable, Value: fmt.Sprintf("127.0.0.1:%d", AgentPort)})
	worker.VolumeMounts = append(worker.VolumeMounts, pack)
	image := k.Agent.Image
	if image == "" {
		image = k.Image
	}
	command := k.Agent.Command
	if len(command) == 0 {
		command = []string{"copilot"}
	}
	args := append(slices.Clone(command[1:]), "--headless", "--no-auto-update",
		"--port", fmt.Sprint(AgentPort), "--auth-token-env", CopilotTokenSecret, "--no-auto-login")
	env := []corev1.EnvVar{fromSecret(CopilotTokenSecret), fromSecret(AgentConnectionSecret),
		{Name: "COPILOT_HOME", Value: podTemp + "/copilot"}, {Name: "COPILOT_DISABLE_KEYTAR", Value: "1"},
		{Name: "XDG_CACHE_HOME", Value: podTemp + "/cache"}, {Name: "NO_PROXY", Value: "127.0.0.1,localhost"}}
	if k.Agent.Proxy != "" {
		env = append(env, corev1.EnvVar{Name: "HTTPS_PROXY", Value: k.Agent.Proxy},
			corev1.EnvVar{Name: "HTTP_PROXY", Value: k.Agent.Proxy})
	}
	pod.InitContainers = append(pod.InitContainers, corev1.Container{
		Name: "agent", Image: image, Command: command[:1], Args: args, Env: env,
		Resources: k.Agent.Resources, RestartPolicy: &always,
		TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
		StartupProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{
			Host: "127.0.0.1", Port: intstr.FromInt32(AgentPort)}}, PeriodSeconds: 1, FailureThreshold: 120},
		SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: &no, ReadOnlyRootFilesystem: &yes,
			Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}},
		VolumeMounts: agentMounts(pack, sealed),
	})
	pod.Volumes = append(pod.Volumes, corev1.Volume{Name: "agent-tmp",
		VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}})
	pod.TerminationGracePeriodSeconds = &grace
}

// agentMounts are what a session's agent mounts: the pack, its own /tmp, and the scratch it shares with
// the worker; sealed, the pack read-only and no scratch.
func agentMounts(pack corev1.VolumeMount, sealed bool) []corev1.VolumeMount {
	own := corev1.VolumeMount{Name: "agent-tmp", MountPath: podTemp}
	if sealed {
		pack.ReadOnly = true
		return []corev1.VolumeMount{pack, own}
	}
	return []corev1.VolumeMount{{Name: "scratch", MountPath: podWork}, pack, own}
}

// Wait polls the Job until it succeeds or fails, and names the worker's own last words on failure.
func (k *Kubernetes) Wait(ctx context.Context, attempt string) error {
	name := k.name(attempt)
	poll := k.Poll
	if poll <= 0 {
		poll = 2 * time.Second
	}
	for {
		job, err := k.Client.BatchV1().Jobs(k.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("reading job %s: %w", name, err)
		}
		if job.Status.Succeeded > 0 {
			return nil
		}
		if job.Status.Failed > 0 || jobFailed(job) {
			return fmt.Errorf("job %s failed: %s", name, k.lastWords(ctx, attempt))
		}
		if k.stopped(ctx, job, attempt) {
			return ErrStopped
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// Stop suspends the attempt's Job, which sends its pod SIGTERM and gives the worker the pod's grace
// period to report what it did; Wait then answers ErrStopped once the pod is gone.
func (k *Kubernetes) Stop(ctx context.Context, attempt string) error {
	suspend := []byte(`{"spec":{"suspend":true}}`)
	_, err := k.Client.BatchV1().Jobs(k.Namespace).Patch(ctx, k.name(attempt), types.MergePatchType, suspend, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("stopping job %s: %w", k.name(attempt), err)
	}
	return nil
}

// stopped reports whether a Job Stop suspended has no pod left: its worker has finished, whatever it
// managed to do.
func (k *Kubernetes) stopped(ctx context.Context, job *batchv1.Job, attempt string) bool {
	if job.Spec.Suspend == nil || !*job.Spec.Suspend {
		return false
	}
	pods, err := k.Client.CoreV1().Pods(k.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "abom.github.com/attempt=" + attempt})
	return err == nil && len(pods.Items) == 0
}

func jobFailed(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func (k *Kubernetes) lastWords(ctx context.Context, attempt string) string {
	pods, err := k.Client.CoreV1().Pods(k.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "abom.github.com/attempt=" + attempt})
	if err != nil || len(pods.Items) == 0 {
		return "no pod to read"
	}
	for _, s := range pods.Items[0].Status.ContainerStatuses {
		if t := s.State.Terminated; t != nil {
			return strings.TrimSpace(t.Reason + ": " + t.Message)
		}
	}
	return string(pods.Items[0].Status.Phase)
}

func (k *Kubernetes) poolLabels() map[string]string {
	labels := map[string]string{"app.kubernetes.io/name": "codeql-abom", "app.kubernetes.io/component": "worker",
		"abom.github.com/pool": k.Pool}
	if k.Instance != "" {
		labels["app.kubernetes.io/instance"] = k.Instance
	}
	return labels
}

// Reap deletes the pool's Jobs a previous coordinator left running, and their Secrets by name, as
// Cleanup does, since the token one holds is as live as a running attempt's. Only the coordinator
// holding the lease may call it: their claims were released, so nobody will collect what they produce.
func (k *Kubernetes) Reap(ctx context.Context) (int, error) {
	jobs, err := k.Client.BatchV1().Jobs(k.Namespace).List(ctx,
		metav1.ListOptions{LabelSelector: labels.SelectorFromSet(k.poolLabels()).String()})
	if err != nil {
		return 0, fmt.Errorf("listing the %s pool's jobs: %w", k.Pool, err)
	}
	background := metav1.DeletePropagationBackground
	reaped := 0
	for _, job := range jobs.Items {
		err := k.Client.BatchV1().Jobs(k.Namespace).Delete(ctx, job.Name, metav1.DeleteOptions{PropagationPolicy: &background})
		if err != nil && !apierrors.IsNotFound(err) {
			return reaped, fmt.Errorf("deleting leftover job %s: %w", job.Name, err)
		}
		err = k.Client.CoreV1().Secrets(k.Namespace).Delete(ctx, job.Name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return reaped, fmt.Errorf("deleting leftover job %s's secret: %w", job.Name, err)
		}
		reaped++
	}
	return reaped, nil
}

// Cleanup deletes the Job, its pod and its Secret. The Secret is deleted by name as well as by its
// owner, so the token it holds does not wait on garbage collection.
func (k *Kubernetes) Cleanup(ctx context.Context, attempt string) {
	background := metav1.DeletePropagationBackground
	_ = k.Client.BatchV1().Jobs(k.Namespace).Delete(ctx, k.name(attempt), metav1.DeleteOptions{PropagationPolicy: &background})
	_ = k.Client.CoreV1().Secrets(k.Namespace).Delete(ctx, k.name(attempt), metav1.DeleteOptions{})
}

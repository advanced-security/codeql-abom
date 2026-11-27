package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
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
)

// Kubernetes launches each attempt as a Job in one namespace, running the pool's ServiceAccount
// with its SPIFFE identity mounted by csi-driver-spiffe. The grant and any lent token travel in a
// Secret the Job owns from the moment the Secret exists, so both go when the Job does.
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
	// Env and EnvFrom carry the object store's configuration and credentials.
	Env       []corev1.EnvVar
	EnvFrom   []corev1.EnvFromSource
	Resources corev1.ResourceRequirements
	// PriorityClass ranks attempts below the coordinator and the database, so under pressure the
	// scheduler and the kubelet give way with analysis first.
	PriorityClass string
	Deadline      time.Duration
	// Args are added to the worker command, such as CodeQL's thread and RAM budget.
	Args []string
	Poll time.Duration
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
	job, err := k.Client.BatchV1().Jobs(k.Namespace).Create(ctx, k.job(name, labels, l.Secrets), metav1.CreateOptions{})
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

func (k *Kubernetes) job(name string, labels map[string]string, secrets map[string]string) *batchv1.Job {
	no, yes := false, true
	uid := int64(10001)
	deadline := int64(k.Deadline / time.Second)
	ttl := int32(600)
	backoff := int32(0)
	// The lock every CodeQL invocation takes is made in the user's cache directory, beneath the
	// read-only HOME unless it is moved.
	env := append([]corev1.EnvVar{{Name: "XDG_CACHE_HOME", Value: podTemp + "/cache"}}, k.Env...)
	if _, ok := secrets[TokenSecret]; ok {
		env = append(env, corev1.EnvVar{Name: TokenSecret, ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: TokenSecret}}})
	}
	args := append([]string{"worker", "--grant", podGrant + "/grant.json", "--trust", podTrust, "--svid", podSVID,
		"--work-dir", podWork}, k.Args...)
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: k.Namespace, Labels: labels},
		Spec: batchv1.JobSpec{
			Suspend:      &yes,
			BackoffLimit: &backoff, ActiveDeadlineSeconds: &deadline, TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					ServiceAccountName:           k.ServiceAccount,
					PriorityClassName:            k.PriorityClass,
					AutomountServiceAccountToken: &no,
					SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: &yes, RunAsUser: &uid, RunAsGroup: &uid,
						FSGroup: &uid, SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
					Containers: []corev1.Container{{
						Name: "worker", Image: k.Image, Args: args, Env: env, EnvFrom: k.EnvFrom, Resources: k.Resources,
						TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
						SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: &no, ReadOnlyRootFilesystem: &yes,
							Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "grant", MountPath: podGrant, ReadOnly: true},
							{Name: "trust", MountPath: podTrust, ReadOnly: true},
							{Name: "spiffe", MountPath: podSVID, ReadOnly: true},
							{Name: "scratch", MountPath: podWork},
							{Name: "tmp", MountPath: podTemp},
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
					},
				},
			},
		},
	}
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
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
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

// Reap deletes the pool's Jobs a previous coordinator left running. Only the coordinator holding
// the lease may call it: their claims were released, so nobody will collect what they produce.
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

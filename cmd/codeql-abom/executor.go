package main

import (
	"context"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/execution"
	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/serve"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
)

// executorConfig says where a batch's fetch and CodeQL work runs.
type executorConfig struct {
	mode           string
	namespace      string
	instance       string
	image          string
	trustDomain    string
	trustConfigMap string
	spiffeRoots    string
	s3Secret       string
	priorityClass  string
	// analysis is the crawl's pool; tests and runs are the pools a query's own tests and requested
	// runs use, sized apart from it. Neither of those has a limit flag: each Job may use what it
	// requests. authoring runs sessions, each a Job whose pod runs the Copilot CLI beside the worker.
	analysis, tests, runs, authoring poolFlags
	// agentCommand starts the Copilot CLI in a session's pod, from agentImage; qlmcp is the command line
	// that starts ql-mcp there.
	agentCommand, agentImage, qlmcp string
	// noProxy are the hosts every pool's workers reach directly rather than through their proxy.
	noProxy string
}

// poolFlags size one pool's Jobs, as the flags named with prefix set them, and name the proxy they
// reach out through.
type poolFlags struct {
	name, prefix   string
	serviceAccount string
	cpu, cpuLimit  string
	memory         string
	timeout        time.Duration
	proxy          string
}

func addExecutorFlags(fs *flag.FlagSet) *executorConfig {
	c := &executorConfig{
		analysis:  poolFlags{prefix: "worker-"},
		tests:     poolFlags{name: "qlaas-test", prefix: "test-worker-"},
		runs:      poolFlags{name: "qlaas-run", prefix: "run-worker-"},
		authoring: poolFlags{name: "authoring", prefix: "authoring-worker-"},
	}
	fs.StringVar(&c.mode, "executor", "local", "where analysis runs: local, in this process, or kubernetes, as one Job per repository")
	fs.StringVar(&c.namespace, "worker-namespace", "", "namespace analysis Jobs run in (default: this pod's)")
	fs.StringVar(&c.instance, "worker-instance", "", "the release analysis Jobs are labeled with, so releases sharing a namespace reap only their own")
	fs.StringVar(&c.image, "worker-image", os.Getenv("ABOM_WORKER_IMAGE"), "image analysis Jobs run (default: $ABOM_WORKER_IMAGE)")
	fs.StringVar(&c.analysis.name, "worker-pool", "discovery", "the worker pool analysis Jobs belong to")
	fs.StringVar(&c.analysis.serviceAccount, "worker-service-account", "abom-worker-discovery", "ServiceAccount analysis Jobs run as")
	fs.StringVar(&c.trustDomain, "spiffe-trust-domain", "cluster.local", "SPIFFE trust domain workload identities are issued in")
	fs.StringVar(&c.trustConfigMap, "worker-trust-configmap", "abom-decision-trust", "ConfigMap this server publishes its decision bundle to")
	fs.StringVar(&c.spiffeRoots, "spiffe-roots", "/etc/abom/spiffe-roots/ca.crt", "CA certificates a worker's identity must chain to")
	fs.StringVar(&c.s3Secret, "worker-s3-secret", "", "Secret holding the object store credentials analysis Jobs read")
	fs.StringVar(&c.analysis.cpu, "worker-cpu", "2", "CPU each analysis Job requests")
	fs.StringVar(&c.analysis.cpuLimit, "worker-cpu-limit", "", "CPU each analysis Job may use (default: --worker-cpu; 0 for no limit)")
	fs.StringVar(&c.priorityClass, "worker-priority-class", "", "PriorityClass analysis Jobs run at, below the coordinator's")
	fs.StringVar(&c.analysis.memory, "worker-memory", "4Gi", "memory each analysis Job requests and is limited to")
	fs.DurationVar(&c.analysis.timeout, "worker-timeout", time.Hour, "longest an analysis Job may take, and its grant lasts")
	fs.StringVar(&c.tests.serviceAccount, "test-worker-service-account", "abom-worker-qlaas-test",
		"ServiceAccount the Jobs running a query's tests run as")
	fs.StringVar(&c.tests.cpu, "test-worker-cpu", "1", "CPU each test Job requests and may use")
	fs.StringVar(&c.tests.memory, "test-worker-memory", "2Gi", "memory each test Job requests and is limited to")
	fs.DurationVar(&c.tests.timeout, "test-worker-timeout", 30*time.Minute, "longest a test Job may take, and its grant lasts")
	fs.StringVar(&c.runs.serviceAccount, "run-worker-service-account", "abom-worker-qlaas-run",
		"ServiceAccount the Jobs analyzing a requested run's repositories run as")
	fs.StringVar(&c.runs.cpu, "run-worker-cpu", "2", "CPU each run Job requests and may use")
	fs.StringVar(&c.runs.memory, "run-worker-memory", "4Gi", "memory each run Job requests and is limited to")
	fs.DurationVar(&c.runs.timeout, "run-worker-timeout", time.Hour, "longest a run Job may take, and its grant lasts")
	fs.StringVar(&c.authoring.serviceAccount, "authoring-worker-service-account", "abom-worker-authoring",
		"ServiceAccount the Jobs running authoring sessions run as")
	fs.StringVar(&c.authoring.cpu, "authoring-worker-cpu", "1",
		"CPU a session's worker, and its agent beside it, each request and may use")
	fs.StringVar(&c.authoring.memory, "authoring-worker-memory", "3Gi",
		"memory a session's worker, and its agent beside it, each request and are limited to")
	fs.DurationVar(&c.authoring.timeout, "authoring-worker-timeout", time.Hour,
		"longest a session's Job may take, and its grant lasts; a session also ends before its Copilot token")
	fs.StringVar(&c.agentCommand, "authoring-agent-command", "copilot",
		"command that starts the Copilot CLI in a session's pod")
	fs.StringVar(&c.agentImage, "authoring-agent-image", "",
		"image the Copilot CLI runs from in a session's pod (default: --worker-image)")
	fs.StringVar(&c.authoring.proxy, "authoring-egress-proxy", "",
		"proxy, as a URL, a session's agent reaches Copilot through; the only way out of its pod")
	fs.StringVar(&c.analysis.proxy, "worker-egress-proxy", "",
		"proxy, as a URL, analysis Jobs reach out through (default: none, and each reaches out directly)")
	fs.StringVar(&c.tests.proxy, "test-worker-egress-proxy", "", "proxy, as a URL, the Jobs running a query's tests reach out through")
	fs.StringVar(&c.runs.proxy, "run-worker-egress-proxy", "", "proxy, as a URL, the Jobs analyzing a requested run reach out through")
	fs.StringVar(&c.noProxy, "worker-no-proxy", "",
		"comma-separated hosts every Job reaches directly rather than through its proxy, such as an object store in the cluster")
	return c
}

// pools are the Runners beside the crawl's: a query's tests, requested runs, and authoring sessions.
type pools struct{ tests, runs, authoring *execution.Runner }

// workerTokens lends each worker a read of its own repository through the App. Without an App there
// is nothing to lend: a person's token never leaves this process.
func workerTokens(app *ghapp.App) execution.RepositoryTokens {
	if app == nil {
		return nil
	}
	return app
}

// wireExecutor hands the crawl's analysis to Kubernetes Jobs when asked to, and answers the pools
// a query's own tests and requested runs use. Every Job runs under a grant this server signs, so
// there is no Kubernetes executor without a decision key.
func wireExecutor(ctx context.Context, c executorConfig, loop *supervisor.Supervisor, decisions *authz.Point,
	authzDir string, tokens execution.RepositoryTokens, stdout io.Writer) (pools, error) {
	switch c.mode {
	case "", "local":
		return pools{}, nil
	case "kubernetes":
	default:
		return pools{}, fmt.Errorf("serve: --executor is local or kubernetes, not %q", c.mode)
	}
	if decisions == nil {
		return pools{}, errors.New("serve: analysis Jobs run under grants this server signs; name its key with --authz-dir")
	}
	if c.image == "" {
		return pools{}, errors.New("serve: name the image analysis Jobs run with --worker-image")
	}
	k := cluster{c: c, decisions: decisions, stdout: stdout}
	var err error
	if k.client, k.namespace, err = inCluster(c.namespace); err != nil {
		return pools{}, err
	}
	if err := publishTrust(ctx, k.client, k.namespace, c.trustConfigMap, authzDir); err != nil {
		return pools{}, err
	}
	if k.roots, err = workerRoots(c.spiffeRoots); err != nil {
		return pools{}, err
	}
	if k.objects, err = execution.S3FromEnv(ctx); err != nil {
		return pools{}, fmt.Errorf("serve: %w", err)
	}
	k.env, k.envFrom = objectStoreEnv(c.s3Secret)

	analysis, err := k.newPool(ctx, c.analysis, nil)
	if err != nil {
		return pools{}, err
	}
	analysis.Tokens = tokens
	loop.Expander.Remote = analysis
	var out pools
	if out.tests, err = k.newPool(ctx, c.tests, nil); err != nil {
		return pools{}, err
	}
	if out.runs, err = k.newPool(ctx, c.runs, nil); err != nil {
		return pools{}, err
	}
	out.runs.Tokens = tokens
	if pack := runner.CustomPackRoot(); pack != "" {
		agent := &execution.Agent{Workspace: pack, Command: strings.Fields(c.agentCommand), Image: c.agentImage,
			Proxy: c.authoring.proxy}
		if out.authoring, err = k.newPool(ctx, c.authoring, agent); err != nil {
			return pools{}, err
		}
		fmt.Fprintf(stdout, "serve: authoring sessions run as Jobs as %s, the Copilot CLI beside each\n",
			out.authoring.Identity)
	}
	fmt.Fprintf(stdout, "serve: analysis runs as Jobs in %s, each as %s under a grant this server signs\n",
		k.namespace, analysis.Identity)
	fmt.Fprintf(stdout, "serve: a query's tests run as Jobs as %s, and requested runs as %s\n",
		out.tests.Identity, out.runs.Identity)
	return out, nil
}

// inCluster reaches the Kubernetes API this server runs under, and names the namespace its Jobs run
// in: the one asked for, or its own.
func inCluster(namespace string) (kubernetes.Interface, string, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, "", fmt.Errorf("serve: reaching the Kubernetes API: %w", err)
	}
	// Every attempt's Job is made, read every few seconds and deleted through this one client, whose
	// default of 5 requests a second would hold back every pool past about ten attempts at once. The
	// API server's own fairness bounds what this costs it.
	cfg.QPS, cfg.Burst = 50, 100
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, "", err
	}
	if namespace == "" {
		own, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
		if err != nil {
			return nil, "", fmt.Errorf("serve: name the namespace for analysis Jobs with --worker-namespace: %w", err)
		}
		namespace = strings.TrimSpace(string(own))
	}
	return client, namespace, nil
}

// workerRoots reads the CA certificates a worker's identity must chain to.
func workerRoots(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("serve: reading the roots a worker's identity chains to: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("serve: %s holds no certificate", path)
	}
	return roots, nil
}

// objectStoreEnv is how a worker reaches the object store this server does: the same settings, and
// the credentials from secret rather than from this process.
func objectStoreEnv(secret string) ([]corev1.EnvVar, []corev1.EnvFromSource) {
	var env []corev1.EnvVar
	for _, name := range execution.S3Env {
		if v := os.Getenv(name); v != "" {
			env = append(env, corev1.EnvVar{Name: name, Value: v})
		}
	}
	if secret == "" {
		return env, nil
	}
	return env, []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: secret}}}}
}

// cluster is what every pool's Jobs share: where they run, what they read and write, and the point
// that signs their grants.
type cluster struct {
	c         executorConfig
	client    kubernetes.Interface
	namespace string
	env       []corev1.EnvVar
	envFrom   []corev1.EnvFromSource
	decisions *authz.Point
	objects   *execution.S3Store
	roots     *x509.CertPool
	stdout    io.Writer
}

// newPool is the Runner for one pool's Jobs, once the Jobs a previous server left running in it
// are stopped. A pool given an agent runs sessions, the Copilot CLI beside each worker on its budget.
func (k cluster) newPool(ctx context.Context, p poolFlags, agent *execution.Agent) (*execution.Runner, error) {
	budget, codeqlArgs, err := p.budget()
	if err != nil {
		return nil, fmt.Errorf("serve: %w", err)
	}
	if agent != nil {
		agent.Resources = budget
		codeqlArgs = append(codeqlArgs, "--ql-mcp", k.c.qlmcp)
	}
	identity := fmt.Sprintf("spiffe://%s/ns/%s/sa/%s", k.c.trustDomain, k.namespace, p.serviceAccount)
	launcher := &execution.Kubernetes{Client: k.client, Namespace: k.namespace, Instance: k.c.instance, Image: k.c.image, Pool: p.name,
		ServiceAccount: p.serviceAccount, TrustConfigMap: k.c.trustConfigMap, Env: k.env, EnvFrom: k.envFrom,
		Deadline: p.timeout, Args: codeqlArgs, Resources: budget, PriorityClass: k.c.priorityClass, Agent: agent,
		Proxy: p.proxy, NoProxy: splitList(k.c.noProxy)}
	reaped, err := launcher.Reap(ctx)
	if err != nil {
		return nil, fmt.Errorf("serve: %w", err)
	}
	if reaped > 0 {
		fmt.Fprintf(k.stdout, "serve: stopped %d %s jobs a previous server left running\n", reaped, p.name)
	}
	return &execution.Runner{Point: k.decisions, Objects: k.objects, Roots: k.roots, Pool: p.name, Identity: identity,
		Timeout: p.timeout, Launch: launcher, PackRoot: runner.CustomPackRoot}, nil
}

// remoteTests runs a query's tests in the test pool's Jobs, and everything else a tester does here.
type remoteTests struct {
	serve.QueryTester
	pool *execution.Runner
}

func (t remoteTests) RunTests(ctx context.Context, dirs []string) (runner.TestResult, error) {
	return t.pool.RunTests(ctx, runner.CustomPackRoot(), dirs)
}

// publishTrust puts the decision bundle where workers read it. Public keys only: the key stays here.
func publishTrust(ctx context.Context, client kubernetes.Interface, namespace, name, authzDir string) error {
	body, err := os.ReadFile(filepath.Join(authzDir, authz.BundleFile))
	if err != nil {
		return fmt.Errorf("serve: reading the decision bundle: %w", err)
	}
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace,
		Labels: map[string]string{"app.kubernetes.io/name": "codeql-abom"}}, Data: map[string]string{authz.BundleFile: string(body)}}
	configMaps := client.CoreV1().ConfigMaps(namespace)
	_, err = configMaps.Create(ctx, cm, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		_, err = configMaps.Update(ctx, cm, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("serve: publishing the decision bundle: %w", err)
	}
	return nil
}

// budget is what each of the pool's Jobs is given, and the CodeQL budget that fits inside it.
//
// The CPU limit is what keeps analysis off the coordinator's and the database's cores; the RAM
// budget leaves room beside CodeQL's two JVMs for git and the worker itself, since CodeQL would
// otherwise size its heap from the node rather than from the pod.
func (p poolFlags) budget() (corev1.ResourceRequirements, []string, error) {
	refused := func(flag string, err error) (corev1.ResourceRequirements, []string, error) {
		return corev1.ResourceRequirements{}, nil, fmt.Errorf("--%s%s: %w", p.prefix, flag, err)
	}
	if p.timeout <= 0 {
		return corev1.ResourceRequirements{}, nil, fmt.Errorf("--%stimeout must be positive, not %s", p.prefix, p.timeout)
	}
	request, err := resource.ParseQuantity(p.cpu)
	if err != nil {
		return refused("cpu", err)
	}
	limit := request
	if p.cpuLimit != "" {
		if limit, err = resource.ParseQuantity(p.cpuLimit); err != nil {
			return refused("cpu-limit", err)
		}
	}
	mem, err := resource.ParseQuantity(p.memory)
	if err != nil {
		return refused("memory", err)
	}
	out := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: request, corev1.ResourceMemory: mem},
		Limits:   corev1.ResourceList{corev1.ResourceMemory: mem}}
	threads := request.Value()
	if !limit.IsZero() {
		out.Limits[corev1.ResourceCPU] = limit
		threads = limit.Value()
	}
	ram := mem.Value() / (1 << 20) * 3 / 5
	return out, []string{"--threads", fmt.Sprint(max(1, threads)), "--ram", fmt.Sprint(ram)}, nil
}

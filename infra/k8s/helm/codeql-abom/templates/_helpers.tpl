{{- define "abom.name" -}}{{ .Release.Name }}{{- end -}}
{{- define "abom.labels" -}}
app.kubernetes.io/name: codeql-abom
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
{{- /* What selects the coordinator's pod: its Deployment, its Service and the NetworkPolicy. */ -}}
{{- define "abom.coordinatorSelector" -}}
{app.kubernetes.io/instance: {{ .Release.Name }}, app.kubernetes.io/component: coordinator}
{{- end -}}
{{- define "abom.image" -}}{{ .Values.image.repository }}:{{ .Values.image.tag }}{{- end -}}
{{- /* The worker pools, in the order serve names them: each one's key under workers and
   serviceAccounts, its ServiceAccount's name after the release, and the prefix of serve's flags. */ -}}
{{- define "abom.pools" -}}
- {key: discovery, name: discovery, account: worker-discovery, flags: worker, proxyFlag: worker-egress-proxy, port: 3129}
- {key: qlaasTest, name: qlaas-test, account: worker-qlaas-test, flags: test-worker, proxyFlag: test-worker-egress-proxy, port: 3130}
- {key: qlaasRun, name: qlaas-run, account: worker-qlaas-run, flags: run-worker, proxyFlag: run-worker-egress-proxy, port: 3131}
- {key: authoring, name: authoring, account: worker-authoring, flags: authoring-worker, proxyFlag: authoring-egress-proxy, port: 3128}
{{- end -}}
{{- /* The hosts one pool's listener of the egress proxy admits, comma-separated: its own, and every
   pool's, the object store's among them where it is public. Empty is no listener at all. Called with
   (dict "pool" $pool "root" $). */ -}}
{{- define "abom.egressAllow" -}}
{{- $v := .root.Values -}}
{{- $hosts := concat (index $v.egressProxy.allow .pool.key | default list) $v.egressProxy.everyPool -}}
{{- if not $v.objectStore.endpoint -}}
{{- $hosts = append $hosts (printf "%s.s3.%s.amazonaws.com" $v.objectStore.bucket $v.objectStore.region) -}}
{{- end -}}
{{- join "," (uniq $hosts) -}}
{{- end -}}
{{- /* The hosts every worker reaches directly rather than through its proxy, comma-separated: those
   named, and an object store at an endpoint of its own. */ -}}
{{- define "abom.egressDirect" -}}
{{- $direct := .Values.egressProxy.direct -}}
{{- with .Values.objectStore.endpoint }}{{ $direct = append $direct (urlParse .).hostname }}{{ end -}}
{{- join "," (uniq $direct) -}}
{{- end -}}
{{- /* The image an authoring session's agent runs: the image built with --target authoring. */ -}}
{{- define "abom.agentImage" -}}
{{- with .Values.workers.authoring.image }}{{ . }}{{ else }}{{ include "abom.image" . }}-authoring{{ end -}}
{{- end -}}
{{- /* What selects the egress proxy's pod: its Deployment, its Service and the NetworkPolicies. */ -}}
{{- define "abom.egressSelector" -}}
{app.kubernetes.io/instance: {{ .Release.Name }}, app.kubernetes.io/component: egress-proxy}
{{- end -}}
{{- /* The GitHub Apps the coordinator acts as: each one's key in values, the directory it is copied
   into, and serve's flag naming that directory. */ -}}
{{- define "abom.apps" -}}
- {key: githubApp, dir: github-app, flag: app-dir}
- {key: publisherApp, dir: github-app-publisher, flag: publisher-app-dir}
{{- end -}}
{{- /* Non-empty when people sign in, by either provider, so every request needs a session. */ -}}
{{- define "abom.signedIn" -}}{{ if ne .Values.signIn.mode "none" }}true{{ end }}{{- end -}}

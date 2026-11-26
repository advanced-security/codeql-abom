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
- {key: discovery, account: worker-discovery, flags: worker}
- {key: qlaasTest, account: worker-qlaas-test, flags: test-worker}
- {key: qlaasRun, account: worker-qlaas-run, flags: run-worker}
{{- end -}}
{{- /* The GitHub Apps the coordinator acts as: each one's key in values, the directory it is copied
   into, and serve's flag naming that directory. */ -}}
{{- define "abom.apps" -}}
- {key: githubApp, dir: github-app, flag: app-dir}
- {key: publisherApp, dir: github-app-publisher, flag: publisher-app-dir}
{{- end -}}
{{- /* Non-empty when people sign in, by either provider, so every request needs a session. */ -}}
{{- define "abom.signedIn" -}}{{ if ne .Values.signIn.mode "none" }}true{{ end }}{{- end -}}

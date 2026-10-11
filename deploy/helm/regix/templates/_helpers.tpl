{{/* Base name of everything in the release. */}}
{{- define "regix.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 52 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 52 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 52 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "regix.registry.name" -}}{{ include "regix.fullname" . }}-registry{{- end -}}
{{- define "regix.ui.name" -}}{{ include "regix.fullname" . }}-ui{{- end -}}

{{/* Labels. Call with (dict "root" $ "component" "registry"). */}}
{{- define "regix.selectorLabels" -}}
app.kubernetes.io/name: {{ default .root.Chart.Name .root.Values.nameOverride }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{- define "regix.labels" -}}
{{ include "regix.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .root.Chart.Name .root.Chart.Version }}
{{- end -}}

{{/* The Secret holding the registry's htpasswd file. */}}
{{- define "regix.registry.authSecret" -}}
{{- default (printf "%s-auth" (include "regix.registry.name" .)) .Values.registry.auth.existingSecret -}}
{{- end -}}

{{/* The address at which the UI reaches the registry. */}}
{{- define "regix.ui.registryUrl" -}}
{{- if .Values.registry.enabled -}}
http://{{ include "regix.registry.name" . }}.{{ .Release.Namespace }}.svc:{{ .Values.registry.service.port }}
{{- else -}}
{{- required "ui.registryUrl is required when registry.enabled is false" .Values.ui.registryUrl -}}
{{- end -}}
{{- end -}}

{{/* The host people type after `docker pull`. Empty lets Regix use the host of the URL. */}}
{{- define "regix.ui.pullHost" -}}
{{- if .Values.ui.pullHost -}}
{{- .Values.ui.pullHost -}}
{{- else if and .Values.registry.enabled .Values.registry.ingress.enabled -}}
{{- .Values.registry.ingress.host -}}
{{- end -}}
{{- end -}}

{{/* The registry's configuration file: the chart's settings with registry.configOverrides on top. */}}
{{- define "regix.registry.config" -}}
{{- $config := include "regix.registry.baseConfig" . | fromYaml -}}
{{- mustMergeOverwrite $config (deepCopy .Values.registry.configOverrides) | toYaml -}}
{{- end -}}

{{- define "regix.registry.baseConfig" -}}
version: 0.1
log:
  level: info
  formatter: json
storage:
  filesystem:
    rootdirectory: /var/lib/registry
  delete:
    enabled: {{ .Values.registry.deleteEnabled }}
  # No in-memory blob cache: it would keep pointing at layers that garbage collection removed,
  # and later pushes could skip uploading them.
  maintenance:
    uploadpurging:
      enabled: true
      age: 168h
      interval: 24h
      dryrun: false
http:
  addr: ":5000"
  {{- if and .Values.registry.ingress.enabled .Values.registry.ingress.host }}
  host: {{ ternary "https" "http" .Values.registry.ingress.tls.enabled }}://{{ .Values.registry.ingress.host }}
  {{- end }}
  draintimeout: 30s
  headers:
    X-Content-Type-Options: [nosniff]
  debug:
    # Health and metrics; not exposed by the Service.
    addr: ":5001"
    prometheus:
      enabled: true
      path: /metrics
{{- if .Values.registry.auth.enabled }}
auth:
  htpasswd:
    realm: {{ .Values.registry.auth.realm | quote }}
    path: /etc/distribution/auth/htpasswd
{{- end }}
health:
  storagedriver:
    enabled: true
    interval: 30s
    threshold: 3
{{- end -}}

{{/* An Ingress. Call with (dict "root" $ "name" … "component" … "ingress" … "port" …). */}}
{{- define "regix.ingress" -}}
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{ .name }}
  labels:
    {{- include "regix.labels" (dict "root" .root "component" .component) | nindent 4 }}
  {{- with .ingress.annotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
spec:
  {{- with .ingress.className }}
  ingressClassName: {{ . }}
  {{- end }}
  rules:
    - host: {{ required (printf "%s.ingress.host is required when its ingress is enabled" .component) .ingress.host | quote }}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: {{ .name }}
                port:
                  name: http
  {{- if .ingress.tls.enabled }}
  tls:
    - hosts:
        - {{ .ingress.host | quote }}
      secretName: {{ default (printf "%s-tls" .name) .ingress.tls.secretName }}
  {{- end }}
{{- end -}}

{{/* Security settings shared by every container of the chart. */}}
{{- define "regix.containerSecurityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop: [ALL]
{{- end -}}

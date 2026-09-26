{{/* The release's base name, shortened to fit the 63 characters Kubernetes allows */}}
{{- define "umpteenth.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{/* Labels every object of the chart gets, besides the ones that name its component */}}
{{- define "umpteenth.metaLabels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end -}}

{{/* Labels of the server's objects */}}
{{- define "umpteenth.labels" -}}
{{ include "umpteenth.metaLabels" . }}
{{ include "umpteenth.selectorLabels" . }}
{{- end -}}

{{/* Labels that select the Umpteenth pods, which the sandbox NetworkPolicies let sandboxes reach */}}
{{- define "umpteenth.selectorLabels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: server
{{- end -}}

{{- define "umpteenth.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end -}}

{{- define "umpteenth.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{ .Values.serviceAccount.name | default (include "umpteenth.fullname" .) }}
{{- else -}}
{{ .Values.serviceAccount.name | default "default" }}
{{- end -}}
{{- end -}}

{{/* The namespace sandbox and build pods run in */}}
{{- define "umpteenth.sandboxNamespace" -}}
{{ .Values.sandbox.namespace | default .Release.Namespace }}
{{- end -}}

{{/* Whether the chart runs the BuildKit daemon */}}
{{- define "umpteenth.buildkit" -}}
{{- if and .Values.sandbox.buildkit.enabled .Values.sandbox.registry.repository -}}
true
{{- end -}}
{{- end -}}

{{/* The Secret and key the database URL comes from, or nothing for SQLite */}}
{{- define "umpteenth.databaseSecret" -}}
{{- if .Values.database.existingSecret -}}
{{ .Values.database.existingSecret }}
{{- else if .Values.database.connectionString -}}
{{ include "umpteenth.fullname" . }}
{{- else if .Values.postgresql.enabled -}}
{{ include "umpteenth.fullname" . }}-postgresql
{{- end -}}
{{- end -}}

{{- define "umpteenth.databaseSecretKey" -}}
{{- if .Values.database.existingSecret -}}
{{ .Values.database.existingSecretKey }}
{{- else -}}
connection-string
{{- end -}}
{{- end -}}

{{/* A value kept across upgrades: the one already stored in a Secret, or a new random one on install */}}
{{- define "umpteenth.persistedSecret" -}}
{{- $secret := lookup "v1" "Secret" .namespace .name -}}
{{- if and $secret (index $secret.data .key) -}}
{{ index $secret.data .key | b64dec }}
{{- else -}}
{{ randAlphaNum 48 }}
{{- end -}}
{{- end -}}

{{/* Settings that don't fit together fail the install instead of the pods */}}
{{- define "umpteenth.validate" -}}
{{- if not .Values.app.url -}}
{{- fail "app.url is required, e.g. --set app.url=https://umpteenth.example.com" -}}
{{- end -}}
{{- if and (gt (int .Values.replicaCount) 1) (not (include "umpteenth.databaseSecret" .)) -}}
{{- fail "more than one replica needs Postgres: keep postgresql.enabled or set database.connectionString" -}}
{{- end -}}
{{- if and (gt (int .Values.replicaCount) 1) (eq .Values.fileStorage.backend "filesystem") -}}
{{- fail "filesystem storage only works with one replica, use database or s3" -}}
{{- end -}}
{{- end -}}

{{/* Node labels as the comma-separated key=value list sandbox.kubernetes.node_selector reads */}}
{{- define "umpteenth.nodeSelectorList" -}}
{{- $entries := list -}}
{{- range $key, $value := . -}}
{{- $entries = append $entries (printf "%s=%s" $key $value) -}}
{{- end -}}
{{ join "," $entries }}
{{- end -}}

{{/* Tolerations in the taint syntax sandbox.kubernetes.tolerations reads: key=value:Effect, key:Effect or key */}}
{{- define "umpteenth.tolerationList" -}}
{{- $entries := list -}}
{{- range . -}}
{{- $entry := .key -}}
{{- if and .value (ne (.operator | default "Equal") "Exists") -}}
{{- $entry = printf "%s=%s" $entry .value -}}
{{- end -}}
{{- if .effect -}}
{{- $entry = printf "%s:%s" $entry .effect -}}
{{- end -}}
{{- $entries = append $entries $entry -}}
{{- end -}}
{{ join "," $entries }}
{{- end -}}

{{/* An egress rule to the cluster DNS */}}
{{- define "umpteenth.dnsEgress" -}}
- to:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: {{ .namespace }}
      podSelector:
        matchLabels:
          {{- toYaml .podLabels | nindent 10 }}
  ports:
    - protocol: UDP
      port: 53
    - protocol: TCP
      port: 53
{{- end -}}

{{- define "assops.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "assops.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := include "assops.name" . -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "assops.labels" -}}
app.kubernetes.io/name: {{ include "assops.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "assops.selectorLabels" -}}
app.kubernetes.io/name: {{ include "assops.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "assops.image" -}}
{{- $root := index . 0 -}}
{{- $image := index . 1 -}}
{{- if $root.Values.image.registry -}}{{ $root.Values.image.registry }}/{{- end -}}{{ $image.repository }}:{{ $image.tag }}
{{- end -}}

{{- define "assops.secretName" -}}
{{- if .Values.secret.name -}}{{ .Values.secret.name }}{{- else -}}{{ include "assops.fullname" . }}-secret{{- end -}}
{{- end -}}

{{- define "assops.serviceAccountName" -}}
{{- if .Values.serviceAccount.name -}}{{ .Values.serviceAccount.name }}{{- else -}}{{ include "assops.fullname" . }}{{- end -}}
{{- end -}}

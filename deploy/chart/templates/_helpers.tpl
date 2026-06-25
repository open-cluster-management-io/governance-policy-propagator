{{/*
Deployment and webhook selector labels.
*/}}
{{- define "controller.workloadLabels" -}}
{{- range $key := list "name" "webhook-origin" }}
{{- if hasKey $.Values.podLabels $key }}
{{- fail (printf "podLabels.%s is reserved and may not be redefined" $key) }}
{{- end }}
{{- end }}
{{- range $key, $value := .Values.podLabels }}
{{ $key }}: {{ $value | quote }}
{{- end }}
{{- end }}

{{/*
Deployment and webhook annotations.
*/}}
{{- define "controller.workloadAnnotations" -}}
{{- range $key := list "kubectl.kubernetes.io/default-container" }}
{{- if hasKey $.Values.podAnnotations $key }}
{{- fail (printf "podAnnotations.%s is reserved and may not be redefined" $key) }}
{{- end }}
{{- end }}
{{- range $key, $value := .Values.podAnnotations }}
{{ $key }}: {{ $value | quote }}
{{- end }}
{{- end }}

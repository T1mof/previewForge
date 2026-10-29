{{- define "previewforge.labels" -}}
app.kubernetes.io/part-of: previewforge
app.kubernetes.io/instance: {{ .Release.Name | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service | quote }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | quote }}
{{- end -}}
{{- define "previewforge.image" -}}
{{- if .Values.image.digest -}}
{{ printf "%s@%s" .Values.image.repository .Values.image.digest }}
{{- else -}}
{{ printf "%s:%s" .Values.image.repository .Values.image.tag }}
{{- end -}}
{{- end -}}
{{- define "previewforge.operatorIdentity" -}}
{{ printf "system:serviceaccount:%s:previewforge-controller-manager" .Release.Namespace }}
{{- end -}}
{{- define "previewforge.identityExpression" -}}
{{ printf "request.userInfo.username == '%s'" (include "previewforge.operatorIdentity" .) }}
{{- end -}}

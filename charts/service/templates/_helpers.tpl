{{- define "platform-service.labels" -}}
app.kubernetes.io/name: {{ .Values.service.name | quote }}
app.kubernetes.io/instance: {{ .Release.Name | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service | quote }}
app.kubernetes.io/part-of: platform-idp
platform.idp/owner: {{ .Values.service.owner | quote }}
platform.idp/environment: {{ .Values.service.environment | quote }}
platform.idp/metrics-enabled: "true"
{{- end }}

{{- define "platform-service.validate" -}}
{{- $seenEnvironment := dict -}}
{{- range $name, $_ := .Values.deployment.environment -}}
{{- $_ := set $seenEnvironment $name true -}}
{{- end -}}
{{- $seenSecrets := dict -}}
{{- $seenSecretEnvironment := dict -}}
{{- $databaseURLFound := false -}}
{{- range .Values.secrets -}}
{{- if hasKey $seenEnvironment .environmentVariable -}}
{{- fail (printf "environment variable %s is defined as both plain text and a secret" .environmentVariable) -}}
{{- end -}}
{{- if hasKey $seenSecrets .secretName -}}
{{- fail (printf "secretName %s must be unique" .secretName) -}}
{{- end -}}
{{- if hasKey $seenSecretEnvironment .environmentVariable -}}
{{- fail (printf "secret environment variable %s must be unique" .environmentVariable) -}}
{{- end -}}
{{- $_ := set $seenSecrets .secretName true -}}
{{- $_ := set $seenSecretEnvironment .environmentVariable true -}}
{{- if and (eq .environmentVariable "DATABASE_URL") (eq .secretName $.Values.database.secretName) (eq .key "url") -}}
{{- $databaseURLFound = true -}}
{{- end -}}
{{- end -}}
{{- if and (eq .Values.database.engine "postgres") (not $databaseURLFound) -}}
{{- fail "postgres requires a DATABASE_URL secret mapping whose secretName matches database.secretName and whose key is url" -}}
{{- end -}}
{{- end }}

{{- define "platform-service.selectorLabels" -}}
app.kubernetes.io/name: {{ .Values.service.name | quote }}
app.kubernetes.io/instance: {{ .Release.Name | quote }}
{{- end }}

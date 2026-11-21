{{- define "database-provisioner.name" -}}
{{- printf "database-%s-%s" .Values.request.service (sha256sum .Values.request.id | trunc 8) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

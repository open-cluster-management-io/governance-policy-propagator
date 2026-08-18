#! /bin/bash

set -euo pipefail

BASE_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../" >/dev/null 2>&1 && pwd)
CHART_DIR="${BASE_DIR}/deploy/chart"
OPERATOR_YAML="${BASE_DIR}/deploy/operator.yaml"
WEBHOOK_YAML="${BASE_DIR}/deploy/webhook.yaml"
NAMESPACE="open-cluster-management"

echo "Validating Helm chart contents..."

function report_result() {
  local description=$1
  local exit_status=$2
  local output=$3

  if [ "${exit_status}" -eq 0 ]; then
    if [ -n "${output}" ]; then
      echo "${output}"
    fi
    echo "✅ ${description} PASSED"
    return 0
  fi

  echo "❌ ${description} FAILED"
  if [ -n "${output}" ]; then
    echo "${output}"
  fi
  exit 1
}

function validate_cmd() {
  local description=$1
  shift
  local output
  local status=0
  output=$("$@") || status=$?
  report_result "${description}" "${status}" "${output}"
}

function validate_silent() {
  local description=$1
  shift
  local status=0
  "$@" >/dev/null || status=$?
  report_result "${description}" "${status}" ""
}

function validate_diff() {
  local description=${1}
  local content1=${2//\"/}
  local content2=${3//\"/}
  local unified_context=${4:-3}
  local output
  local status=0
  output=$(diff -u"${unified_context}" -L "Helm chart" -L "Operator YAML" \
    <(printf '%s\n' "${content1}") \
    <(printf '%s\n' "${content2}")) || status=$?
  report_result "${description}" "${status}" "${output}"
}

echo "=== Lint Helm chart"

helm_args=("--set" "args.enableWebhooks=true")

validate_cmd "Helm chart linting" helm lint "${helm_args[@]}" "${CHART_DIR}"
validate_silent "Helm template validation" helm template test "${helm_args[@]}" "${CHART_DIR}" -n "${NAMESPACE}"

rendered_chart=$(helm template test "${helm_args[@]}" "${CHART_DIR}" -n "${NAMESPACE}")
query='[.apiVersion + "/" + .kind + "/" + .metadata.name].[]'
chart_resources=$(echo "${rendered_chart}" | yq ea "${query}" | sort)
operator_resources=$(yq ea "${query}" "${OPERATOR_YAML}" "${WEBHOOK_YAML}" | sort)

echo "=== Compare Helm and Kustomize output"
validate_diff "Diff chart and operator kinds" "${chart_resources}" "${operator_resources}" 1

for resource in $(echo "${rendered_chart}" | yq ea '[.kind].[]'); do
  query="select(.kind == \"${resource}\") | del(.metadata.namespace)"
  chart_resource=$(echo "${rendered_chart}" | yq "${query}" | grep -v '^# Source: ')
  operator_resource=$(yq "${query}" "${OPERATOR_YAML}" "${WEBHOOK_YAML}")
  validate_diff "Diff ${resource} YAML" "${chart_resource}" "${operator_resource}"
done

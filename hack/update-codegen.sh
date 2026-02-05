#!/usr/bin/env bash
# Copyright 2020 The Karmada Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.


set -o errexit
set -o nounset
set -o pipefail

# For all commands, the working directory is the parent directory(repo root).
REPO_ROOT=$(git rev-parse --show-toplevel)
cd "${REPO_ROOT}"

GO111MODULE=on go install k8s.io/code-generator/cmd/deepcopy-gen
GO111MODULE=on go install k8s.io/code-generator/cmd/conversion-gen
CODEGEN_PKG=${CODEGEN_PKG:-$(ls -d -1 ./vendor/k8s.io/code-generator 2>/dev/null || echo ../code-generator)}
source "${CODEGEN_PKG}/kube_codegen.sh"

export GOPATH=$(go env GOPATH | awk -F ':' '{print $1}')
export PATH=$PATH:$GOPATH/bin

go_path="${REPO_ROOT}/_go"
cleanup() {
  rm -rf "${go_path}"
}
trap "cleanup" EXIT SIGINT

cleanup

source "${REPO_ROOT}"/hack/util.sh
util:create_gopath_tree "${REPO_ROOT}" "${go_path}"
export GOPATH="${go_path}"

echo "Generating with deepcopy-gen"
deepcopy-gen \
  --go-header-file hack/boilerplate/boilerplate.go.txt \
  --output-file=zz_generated.deepcopy.go \
  github.com/karmada-io/karmada/pkg/apis/cluster/v1alpha1
deepcopy-gen \
  --go-header-file hack/boilerplate/boilerplate.go.txt \
  --output-file=zz_generated.deepcopy.go \
  github.com/karmada-io/karmada/pkg/apis/cluster
deepcopy-gen \
  --go-header-file hack/boilerplate/boilerplate.go.txt \
  --output-file=zz_generated.deepcopy.go \
  github.com/karmada-io/karmada/pkg/apis/policy/v1alpha1
deepcopy-gen \
  --go-header-file hack/boilerplate/boilerplate.go.txt \
  --output-file=zz_generated.deepcopy.go \
  github.com/karmada-io/karmada/pkg/apis/work/v1alpha1
deepcopy-gen \
  --go-header-file hack/boilerplate/boilerplate.go.txt \
  --output-file=zz_generated.deepcopy.go \
  github.com/karmada-io/karmada/pkg/apis/work/v1alpha2
deepcopy-gen \
  --go-header-file hack/boilerplate/boilerplate.go.txt \
  --output-file=zz_generated.deepcopy.go \
  github.com/karmada-io/karmada/pkg/apis/config/v1alpha1
deepcopy-gen \
  --go-header-file hack/boilerplate/boilerplate.go.txt \
  --output-file=zz_generated.deepcopy.go \
  github.com/karmada-io/karmada/pkg/apis/networking/v1alpha1
deepcopy-gen \
  --go-header-file hack/boilerplate/boilerplate.go.txt \
  --output-file=zz_generated.deepcopy.go \
  github.com/karmada-io/karmada/examples/customresourceinterpreter/apis/workload/v1alpha1
deepcopy-gen \
  --go-header-file hack/boilerplate/boilerplate.go.txt \
  --output-file=zz_generated.deepcopy.go \
  github.com/karmada-io/karmada/pkg/apis/search/v1alpha1
deepcopy-gen \
  --go-header-file hack/boilerplate/boilerplate.go.txt \
  --output-file=zz_generated.deepcopy.go \
  github.com/karmada-io/karmada/pkg/apis/search
deepcopy-gen \
  --go-header-file hack/boilerplate/boilerplate.go.txt \
  --output-file=zz_generated.deepcopy.go \
  github.com/karmada-io/karmada/pkg/apis/autoscaling/v1alpha1
deepcopy-gen \
  --go-header-file hack/boilerplate/boilerplate.go.txt \
  --output-file=zz_generated.deepcopy.go \
  github.com/karmada-io/karmada/operator/pkg/apis/operator/v1alpha1
deepcopy-gen \
  --go-header-file hack/boilerplate/boilerplate.go.txt \
  --output-file=zz_generated.deepcopy.go \
  github.com/karmada-io/karmada/pkg/apis/remedy/v1alpha1
deepcopy-gen \
  --go-header-file hack/boilerplate/boilerplate.go.txt \
  --output-file=zz_generated.deepcopy.go \
  github.com/karmada-io/karmada/pkg/apis/apps/v1alpha1

echo "Generating with conversion-gen"
conversion-gen \
  --go-header-file hack/boilerplate/boilerplate.go.txt \
  --output-file=zz_generated.conversion.go \
  github.com/karmada-io/karmada/pkg/apis/cluster/v1alpha1
conversion-gen \
  --go-header-file hack/boilerplate/boilerplate.go.txt \
  --output-file=zz_generated.conversion.go \
  github.com/karmada-io/karmada/pkg/apis/search/v1alpha1

echo "Generating with register-gen"
kube::codegen::gen_register \
    --boilerplate hack/boilerplate/boilerplate.go.txt \
    "pkg/apis"
kube::codegen::gen_register \
    --boilerplate hack/boilerplate/boilerplate.go.txt \
    "operator/pkg/apis"

echo "Generating with openapi-gen"
kube::codegen::gen_openapi \
    --output-dir "pkg/generated/openapi" \
    --output-pkg "github.com/karmada-io/karmada/pkg/generated/openapi" \
    --report-filename "pkg/generated/openapi/api_rule_violation_report.list" \
    --update-report \
    --boilerplate "hack/boilerplate/boilerplate.go.txt" \
    "pkg/apis"
kube::codegen::gen_openapi \
    --output-dir "operator/pkg/generated/openapi" \
    --output-pkg "github.com/karmada-io/karmada/operator/pkg/generated/openapi" \
    --report-filename "operator/pkg/generated/openapi/api_rule_violation_report.list" \
    --update-report \
    --update-report \
    --boilerplate "hack/boilerplate/boilerplate.go.txt" \
    "operator/pkg/apis"

EXTERNAL_APPLY_CONFIGS="k8s.io/api/core/v1.Taint:k8s.io/client-go/applyconfigurations/core/v1,\
k8s.io/api/core/v1.Toleration:k8s.io/client-go/applyconfigurations/core/v1,\
k8s.io/api/core/v1.NodeSelector:k8s.io/client-go/applyconfigurations/core/v1,\
k8s.io/api/core/v1.NodeSelectorRequirement:k8s.io/client-go/applyconfigurations/core/v1,\
k8s.io/api/core/v1.NodeSelectorTerm:k8s.io/client-go/applyconfigurations/core/v1,\
k8s.io/api/core/v1.ServiceStatus:k8s.io/client-go/applyconfigurations/core/v1,\
k8s.io/api/core/v1.ResourceQuotaStatus:k8s.io/client-go/applyconfigurations/core/v1,\
k8s.io/api/core/v1.LoadBalancerStatus:k8s.io/client-go/applyconfigurations/core/v1,\
k8s.io/api/core/v1.LoadBalancerIngress:k8s.io/client-go/applyconfigurations/core/v1,\
k8s.io/api/core/v1.ResourceRequirements:k8s.io/client-go/applyconfigurations/core/v1,\
k8s.io/api/core/v1.Affinity:k8s.io/client-go/applyconfigurations/core/v1,\
k8s.io/api/core/v1.PersistentVolumeClaimTemplate:k8s.io/client-go/applyconfigurations/core/v1,\
k8s.io/api/core/v1.HostPathVolumeSource:k8s.io/client-go/applyconfigurations/core/v1,\
k8s.io/api/core/v1.EmptyDirVolumeSource:k8s.io/client-go/applyconfigurations/core/v1,\
k8s.io/api/core/v1.Volume:k8s.io/client-go/applyconfigurations/core/v1,\
k8s.io/api/core/v1.VolumeMount:k8s.io/client-go/applyconfigurations/core/v1,\
k8s.io/api/core/v1.Container:k8s.io/client-go/applyconfigurations/core/v1,\
k8s.io/api/networking/v1.IngressSpec:k8s.io/client-go/applyconfigurations/networking/v1,\
k8s.io/api/networking/v1.IngressStatus:k8s.io/client-go/applyconfigurations/networking/v1,\
k8s.io/api/networking/v1.IngressLoadBalancerStatus:k8s.io/client-go/applyconfigurations/networking/v1,\
k8s.io/api/networking/v1.IngressLoadBalancerIngress:k8s.io/client-go/applyconfigurations/networking/v1,\
k8s.io/api/networking/v1.IngressPortStatus:k8s.io/client-go/applyconfigurations/networking/v1,\
k8s.io/api/autoscaling/v2.HorizontalPodAutoscalerStatus:k8s.io/client-go/applyconfigurations/autoscaling/v2,\
k8s.io/api/autoscaling/v2.CrossVersionObjectReference:k8s.io/client-go/applyconfigurations/autoscaling/v2,\
k8s.io/api/autoscaling/v2.MetricSpec:k8s.io/client-go/applyconfigurations/autoscaling/v2,\
k8s.io/api/autoscaling/v2.HorizontalPodAutoscalerBehavior:k8s.io/client-go/applyconfigurations/autoscaling/v2,\
k8s.io/api/autoscaling/v2.MetricStatus:k8s.io/client-go/applyconfigurations/autoscaling/v2,\
k8s.io/api/autoscaling/v2.HPAScalingRules:k8s.io/client-go/applyconfigurations/autoscaling/v2,\
k8s.io/api/autoscaling/v2.HPAScalingPolicy:k8s.io/client-go/applyconfigurations/autoscaling/v2,\
k8s.io/api/admissionregistration/v1.WebhookClientConfig:k8s.io/client-go/applyconfigurations/admissionregistration/v1,\
k8s.io/api/admissionregistration/v1.ServiceReference:k8s.io/client-go/applyconfigurations/admissionregistration/v1"

echo "Generating with applyconfiguration-gen & client-gen & informer-gen & lister-gen"
kube::codegen::gen_client \
    --with-watch \
    --with-applyconfig \
    --applyconfig-openapi-schema <(go run github.com/karmada-io/karmada/pkg/generated/openapi/cmd/models-schema) \
    --applyconfig-externals "${EXTERNAL_APPLY_CONFIGS}" \
    --applyconfig-name "applyconfigurations" \
    --output-dir "pkg/generated" \
    --output-pkg "github.com/karmada-io/karmada/pkg/generated" \
    --boilerplate "hack/boilerplate/boilerplate.go.txt" \
    "pkg/apis"
kube::codegen::gen_client \
    --with-watch \
    --with-applyconfig \
    --applyconfig-openapi-schema <(go run github.com/karmada-io/karmada/operator/pkg/generated/openapi/cmd/models-schema) \
    --applyconfig-externals "${EXTERNAL_APPLY_CONFIGS}" \
    --applyconfig-name "applyconfigurations" \
    --output-dir "operator/pkg/generated" \
    --output-pkg "github.com/karmada-io/karmada/operator/pkg/generated" \
    --boilerplate "hack/boilerplate/boilerplate.go.txt" \
    "operator/pkg/apis"

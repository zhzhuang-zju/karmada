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

# This script deploy karmada control plane to any cluster you want.	REPO_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
# This script depends on utils in: ${REPO_ROOT}/hack/util.sh

REPO_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
KUBECONFIG_PATH=${KUBECONFIG_PATH:-"${HOME}/.kube"}
CERT_DIR=${CERT_DIR:-"${HOME}/.karmada"}
mkdir -p "${CERT_DIR}" &>/dev/null ||  mkdir -p "${CERT_DIR}"
rm -f "${CERT_DIR}/*" &>/dev/null ||  rm -f "${CERT_DIR}/*"

source ${REPO_ROOT}/hack/util.sh

function usage() {
  echo "This script deploys karmada control plane components to a given cluster."
  echo "Note: This script is an internal script and is not intended used by end-users."
  echo "Usage: hack/deploy-karmada.sh <KUBECONFIG> <CONTEXT_NAME> [HOST_CLUSTER_TYPE]"
  echo "Example: hack/deploy-karmada.sh ~/.kube/config karmada-host local"
  echo -e "Parameters:\n\tKUBECONFIG\t\tYour cluster's kubeconfig that you want to install to"
  echo -e "\tCONTEXT_NAME\t\tThe name of context in 'kubeconfig'"
  echo -e "\tHOST_CLUSTER_TYPE\tThe type of your cluster that will install Karmada. Optional values are 'local' and 'remote',"
  echo -e "\t\t\t\t'local' is default, as that is for the local environment, i.e. for the cluster created by kind."
  echo -e "\t\t\t\tAnd if you want to install karmada to a standalone cluster, set it as 'remote'"
}

# recover the former value of KUBECONFIG
function recover_kubeconfig {
  if [ -n "${CURR_KUBECONFIG+x}" ];then
    export KUBECONFIG="${CURR_KUBECONFIG}"
  else
    unset KUBECONFIG
  fi
}

if [[ $# -lt 2 ]]; then
  usage
  exit 1
fi

# check config file existence
HOST_CLUSTER_KUBECONFIG=$1
if [[ ! -f "${HOST_CLUSTER_KUBECONFIG}" ]]; then
  echo -e "ERROR: failed to get kubernetes config file: '${HOST_CLUSTER_KUBECONFIG}', not existed.\n"
  usage
  exit 1
fi

# check context existence and switch
# backup current kubeconfig before changing KUBECONFIG
if [ -n "${KUBECONFIG+x}" ];then
  CURR_KUBECONFIG=$KUBECONFIG
fi
export KUBECONFIG="${HOST_CLUSTER_KUBECONFIG}"
HOST_CLUSTER_NAME=$2
if ! kubectl config get-contexts "${HOST_CLUSTER_NAME}" > /dev/null 2>&1;
then
  echo -e "ERROR: failed to get context: '${HOST_CLUSTER_NAME}' not in ${HOST_CLUSTER_KUBECONFIG}. \n"
  usage
  recover_kubeconfig
  exit 1
fi

kind load docker-image "${REGISTRY}/karmada-operator:${VERSION}" --name="${HOST_CLUSTER_NAME}"
helm --kubeconfig ${HOST_CLUSTER_KUBECONFIG} install karmada-operator -n karmada-system  --create-namespace --dependency-update ./charts/karmada-operator --debug
kubectl apply -f operator/config/crds/
kubectl create ns test
kubectl apply -f operator/config/samples/karmada.yaml
kubectl wait --for=condition=Ready --timeout=360s karmada karmada-demo -n test

kubectl get secret -n test karmada-demo-admin-config -o jsonpath={.data.kubeconfig} | base64 -d > ~/.kube/karmada-tmp-apiserver.config

cat ~/.kube/karmada-tmp-apiserver.config| grep "client-certificate-data"| awk '{print $2}'| base64 -d  > ${CERT_DIR}/karmada.crt
cat ~/.kube/karmada-tmp-apiserver.config| grep "client-key-data"| awk '{print $2}'| base64 -d  > ${CERT_DIR}/karmada.key
KARMADA_APISERVER=$(cat ~/.kube/karmada-tmp-apiserver.config| grep "server:"| awk '{print $2}')

# write karmada api server config to kubeconfig file
util::append_client_kubeconfig_with_server "${HOST_CLUSTER_KUBECONFIG}" "${CERT_DIR}/karmada.crt" "${CERT_DIR}/karmada.key" "${KARMADA_APISERVER}" karmada-apiserver
rm ~/.kube/karmada-tmp-apiserver.config








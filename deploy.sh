#!/bin/bash

comp=$1

export VERSION="latest"
export REGISTRY="docker.io/karmada"
make image-${comp} GOOS="linux" --directory=.
kind load docker-image docker.io/karmada/${comp}:latest --name karmada-host

export KUBECONFIG=~/.kube/karmada.config
kubectl --context karmada-host  rollout restart  deployment/${comp} -n karmada-system

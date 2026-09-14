#!/bin/bash
current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -ex

kubectl -n default create -f debug-tools.ds.yaml
kubectl -n default create -f nginx-tools.ds.yaml

sed -i  "s!172.20.60.58!$registryServer_ip!" quiq_registry-ui.yaml
kubectl -n default create -f quiq_registry-ui.yaml




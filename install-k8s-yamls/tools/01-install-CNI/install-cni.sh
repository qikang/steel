#!/bin/bash
current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -ex

kubectl create -f flannel/kube-flannel.yaml

# 检查 vxlan 网卡
cni_interface=""
until [[ ! -z $cni_interface ]];do
  echo 'check flannel.1.'
  sleep 5
  cni_interface=$(ip link show type vxlan)
done

ip -d link show flannel.1

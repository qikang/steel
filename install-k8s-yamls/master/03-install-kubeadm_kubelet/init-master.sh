#!/bin/bash
current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -ex

kubeadm_init_yaml="now.kubeadm.init.yaml"

generate_kubeadm_config(){
  # 内置变量 k8s_network_stack
  # ipv4
  if [[ $k8s_network_stack -eq "ipv4" ]];then
    /bin/bash generate_config_ipv4.sh $kubeadm_init_yaml
  # ipv6
  elif [[ $k8s_network_stack -eq "ipv6" ]];then
    echo "no support ipv6"
    exit 1

  # dual-stack
  elif [[ $k8s_network_stack -eq "dual-stack" ]];then
    echo "no support dual-stack"
    exit 1

  fi

}


run_kubeadm_init(){
  kubeadm config images pull --config=$kubeadm_init_yaml
  kubeadm init --config=$kubeadm_init_yaml --ignore-preflight-errors=crictl --upload-certs
  sleep 5
}

set_local_k(){
  mkdir -p $HOME/.kube
  sudo cp -i /etc/kubernetes/admin.conf $HOME/.kube/config
  sudo chown $(id -u):$(id -g) $HOME/.kube/config
}


generate_kubeadm_config
run_kubeadm_init
set_local_k




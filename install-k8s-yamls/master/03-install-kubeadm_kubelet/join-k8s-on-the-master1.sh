#!/bin/bash
current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -xe

mode=$1

# check apiserver ip
check_apiserver_ip_re=$(ip a | grep "$apiserverLB_ip" 2>/dev/null | grep -v grep || true)
if [[ ! -z $check_apiserver_ip_re ]];then
  echo "hostname is `hostname`."
  exit 0
fi

ssh_conn="ssh root@$apiserverLB_ip"

# kubeadm_join_cmd
kubeadm_join_cmd=$($ssh_conn "kubeadm token create --print-join-command --ttl 1h" | tail -n 1)
# kubeadm_cert_key
kubeadm_cert_key=$($ssh_conn "kubeadm init phase upload-certs --upload-certs 2>/dev/null | grep -oE '^[0-9a-f]{64}$' | tail -1")

# kubeadm_join_cmd=$($ssh_conn "kubeadm token create --print-join-command" | tail -n 1)
# kubeadm_cert_key=$($ssh_conn "kubeadm init phase upload-certs --upload-certs 2>/dev/null | grep -oE '^[0-9a-f]{64}$' | tail -1")


echo "等待 API Server 就绪..."
while true; do
    apiserver_check=`curl -s -k https://$apiserverLB_ip:6443/healthz`
    if [[ "$apiserver_check" == "ok" ]]; then
        echo "API Server 已就绪！"

        # waiting pod to running.
        sleep 10

        break

    fi
    echo "API Server 未就绪，等待 5 秒后重试..."
    sleep 5
done

# join control-plane
if [[ $mode == "control-plane" ]];then


  $kubeadm_join_cmd --control-plane --certificate-key $kubeadm_cert_key

  mkdir -p $HOME/.kube
  cp -i /etc/kubernetes/admin.conf $HOME/.kube/config
  chown $(id -u):$(id -g) $HOME/.kube/config

fi

# join worker
if [[ $mode == "worker" ]];then
  $kubeadm_join_cmd
fi

# waiting pod to running.
sleep 10



#!/bin/bash
current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -ex

# mkdir -p rpms/amd64
# cd rpms/amd64
# wget https://mirrors.aliyun.com/kubernetes-new/core/stable/v1.29/rpm/x86_64/cri-tools-1.29.0-150500.1.1.x86_64.rpm
# wget https://mirrors.aliyun.com/kubernetes-new/core/stable/v1.29/rpm/x86_64/kubeadm-1.29.5-150500.1.1.x86_64.rpm
# wget https://mirrors.aliyun.com/kubernetes-new/core/stable/v1.29/rpm/x86_64/kubectl-1.29.5-150500.1.1.x86_64.rpm
# wget https://mirrors.aliyun.com/kubernetes-new/core/stable/v1.29/rpm/x86_64/kubelet-1.29.5-150500.1.1.x86_64.rpm
# cd $current_dir

# mkdir -p rpms/arm64
# cd rpms/arm64
# wget https://mirrors.aliyun.com/kubernetes-new/core/stable/v1.29/rpm/aarch64/cri-tools-1.29.0-150500.1.1.aarch64.rpm
# wget https://mirrors.aliyun.com/kubernetes-new/core/stable/v1.29/rpm/aarch64/kubeadm-1.29.5-150500.1.1.aarch64.rpm
# wget https://mirrors.aliyun.com/kubernetes-new/core/stable/v1.29/rpm/aarch64/kubectl-1.29.5-150500.1.1.aarch64.rpm
# wget https://mirrors.aliyun.com/kubernetes-new/core/stable/v1.29/rpm/aarch64/kubelet-1.29.5-150500.1.1.aarch64.rpm
# cd $current_dir

dnf install -y rpms/$os_arch/*.rpm

systemctl restart kubelet
systemctl enable kubelet

# set kubeadm 100 year
# kubeadm_file="kubeadm-1.29.5-$os_arch-100year"
# mv /usr/bin/kubeadm{,.install}
# cp $kubeadm_file /usr/bin/kubeadm
# chmod +x /usr/bin/kubeadm

echo "install kubelet done"


# set alias k cli.
k_set_file="/root/.bashrc"
check_k_re=$(grep "__start_kubectl" "$k_set_file" 2>/dev/null | grep -v grep || true)

if [[ -z "$check_k_re" ]]; then
    # 添加别名配置
    echo "

source <(kubectl completion bash)
alias k=kubectl
complete -F __start_kubectl k

" | tee -a "$k_set_file"
    
    echo "alias k added to $k_set_file"
else
    echo "alias k already exists in $k_set_file"
fi




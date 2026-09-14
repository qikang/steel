#!/bin/bash
current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -ex

rpms="
cri-tools-1.29.0-150500.1.1.x86_64.rpm
kubeadm-1.29.5-150500.1.1.x86_64.rpm
kubectl-1.29.5-150500.1.1.x86_64.rpm
kubelet-1.29.5-150500.1.1.x86_64.rpm
"

dnf install -y rpms/$os_arch/*.rpm

systemctl restart kubelet
systemctl enable kubelet

# set kubeadm 100 year
kubeadm_file="kubeadm-1.29.5-$os_arch-100year"
mv /usr/bin/kubeadm{,.install}
cp $kubeadm_file /usr/bin/kubeadm
chmod +x /usr/bin/kubeadm

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




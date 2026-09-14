#!/bin/bash
current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -ex


# install nfs
install_nfs(){
  mkdir -p /data/nfs_volumes
  echo "/data/nfs_volumes *(rw,async,no_subtree_check,no_root_squash)" >> /etc/exports
  exportfs -arv
  systemctl enable --now nfs-server
}


# 安装 chart
install_chart(){

  nfs_chart_file="nfs-subdir-external-provisioner.2024-07-18.tar.gz"
  values_file="cloud.values.yaml"

  sed -i "s!172.20.80.1!$nfsServer_ip!g" $values_file
  sed -i "s!nfs-server-host!$nfsServer!g" $values_file
  helm -n kube-system install nfs-provisioner $nfs_chart_file -f $values_file

}

install_nfs
install_chart



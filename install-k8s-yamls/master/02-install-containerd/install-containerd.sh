#!/bin/bash
current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -x

containerd_version="cri-containerd-cni-1.7.25-linux-$os_arch.tar.gz"

tar -zxf $containerd_version -C /

tar -xf containerd.etc.tar.gz -C /

rm -rf /etc/cni/net.d/10-containerd-net.conflist

systemctl enable containerd
systemctl start containerd
systemctl status containerd --no-pager



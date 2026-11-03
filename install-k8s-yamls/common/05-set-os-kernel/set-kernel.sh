#!/bin/bash
current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -x

set_iptables(){
  # 关闭防火墙
  ip6tables -F && ip6tables -t nat -F && ip6tables -t mangle -F && ip6tables -X && ip6tables -Z
  ip6tables -L -nv
  iptables -F && iptables -t nat -F && iptables -t mangle -F && iptables -X && iptables -Z
  iptables -L -nv
  systemctl stop firewalld
  systemctl disable firewalld
}

set_os_base(){

  # selinux
  setenforce 0
  sed -i --follow-symlinks 's/SELINUX=enforcing/SELINUX=disabled/g' /etc/sysconfig/selinux
  cat /etc/sysconfig/selinux

  cp modules.k8s.conf /etc/modules-load.d/k8s.conf
  # 1. 网络和存储相关（Kubernetes 必备）
  modprobe br_netfilter
  modprobe overlay
  modprobe bridge

  # 2. IPVS 相关（用于 kube-proxy 代理模式）
  modprobe ip_vs
  modprobe ip_vs_rr
  modprobe ip_vs_wrr
  modprobe ip_vs_sh

  # 3. 连接跟踪（用 nf_conntrack 替代旧名称，在 OpenEuler 上兼容性更好）
  modprobe nf_conntrack

  cp sysctl.k8s.conf /etc/sysctl.d/k8s.conf  
  sed -i 's/net.ipv4.ip_forward=0/net.ipv4.ip_forward=1/g' /etc/sysctl.conf
  sysctl --system

}

set_iptables
set_os_base



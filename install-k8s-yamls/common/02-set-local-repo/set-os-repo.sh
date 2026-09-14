#!/bin/bash
current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -x

set_repo(){
  # set centos 7.6 yum repo
  mkdir -p /etc/yum.repos.d/bak
  mv /etc/yum.repos.d/*.repo /etc/yum.repos.d/bak/
  cp local_60.86.repo /etc/yum.repos.d/
}

set_swap(){
  # 关闭 swap
  swapoff -a
  sed -ri 's/.*swap.*/#&/' /etc/fstab
  swapon --show
}


set_iptables(){
  # 关闭防火墙
  ip6tables -F && ip6tables -t nat -F && ip6tables -t mangle -F && ip6tables -X && ip6tables -Z
  ip6tables -L -nv
  iptables -F && iptables -t nat -F && iptables -t mangle -F && iptables -X && iptables -Z
  iptables -L -nv
  systemctl stop firewalld
  systemctl disable firewalld
}

set_repo
set_swap
set_iptables
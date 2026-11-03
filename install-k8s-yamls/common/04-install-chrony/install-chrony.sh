#!/bin/bash
current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -x

host_name=$1

install_chrony(){

  # chrony service
  dnf -y install libseccomp chrony vim nc bash-completion nfs-utils tree  

  mv /etc/chrony.conf{,.bak}
  # set chrony.conf
  if [[ "$host_name" == $timeServer ]];then
    #set_chrony
    cat master.chrony.conf > /etc/chrony.conf
  else
    #set_chrony
    cat node.chrony.conf > /etc/chrony.conf
  fi

  # set service.
  systemctl enable chronyd
  systemctl restart chronyd

}


install_chrony

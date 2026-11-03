#!/bin/bash
current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -ex

kubeadm_init_yaml=$1

node_1(){

    # pod network, svc network.
    sed -i "s!10.244.0.0/16!$pod_network_cidr!g" $kubeadm_init_yaml
    sed -i "s!10.96.0.0/16!$service_cidr!g" $kubeadm_init_yaml
    sed -i "s!172.20.80.1!$apiserverLB_ip!g" $kubeadm_init_yaml

}
node_3(){

    # pod network, svc network.
    sed -i "s!10.244.0.0/16!$pod_network_cidr!g" $kubeadm_init_yaml
    sed -i "s!10.96.0.0/16!$service_cidr!g" $kubeadm_init_yaml
    sed -i "s!172.20.80.1!$apiserverLB_ip!g" $kubeadm_init_yaml

    # noapiserverips=192.168.170.48 192.168.170.47
    # 目的是172.20.80.2替换成192.168.170.48,文件是kubeadm_init_yaml="xxxx.yaml"
    # 目的是172.20.80.3替换成192.168.170.49,文件是kubeadm_init_yaml="xxxx.yaml"
    count=2
    # set 80.2 and 80.3 var.
    for ip_nu in $noapiserverips;do
      old_ip="172.20.80.$count"
      sed -i "s!$old_ip!$ip_nu!g" $kubeadm_init_yaml
      ((count++))
    done


}
node_5(){

    # pod network, svc network.
    sed -i "s!10.244.0.0/16!$pod_network_cidr!g" $kubeadm_init_yaml
    sed -i "s!10.96.0.0/16!$service_cidr!g" $kubeadm_init_yaml
    sed -i "s!172.20.80.1!$apiserverLB_ip!g" $kubeadm_init_yaml
    count=5
    # set 80.2 and 80.3 var.
    for ip_nu in $noapiserverips;do
      old_ip="172.20.80.$count"
      sed -i "s!$old_ip!$ip_nu!g" $kubeadm_init_yaml
      ((count++))
    done

}

man(){
  # get kubeadm file.
  if [ $master_node_number = 1 ];then
    source_kubeadm_init_file="ipv4/kubeadm.init.onlyipv4.1.yaml"
    cp $source_kubeadm_init_file $kubeadm_init_yaml
    node_1
  elif [ $master_node_number = 3 ];then
    source_kubeadm_init_file="ipv4/kubeadm.init.onlyipv4.3.yaml"
    cp $source_kubeadm_init_file $kubeadm_init_yaml
    node_3
  elif [ $master_node_number = 5 ];then
    source_kubeadm_init_file="ipv4/kubeadm.init.onlyipv4.5.yaml"
    cp $source_kubeadm_init_file $kubeadm_init_yaml
    node_5
  fi

}

man






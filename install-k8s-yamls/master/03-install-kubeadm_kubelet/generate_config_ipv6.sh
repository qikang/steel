#!/bin/bash
current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -ex

kubeadm_init_yaml='now.kubeadm.init.yaml'

# set apiserver lb addr
apiserver_host="k8s_${apiserverLB_hostName}_ip"
apiserver_ip="${!apiserver_host}"


write_ipv4_config(){


    
    

    # pod network, svc network.
    sed -i "s!10.244.0.0/16!$pod_network_cidr!g" $kubeadm_init_yaml
    sed -i "s!10.96.0.0/16!$service_cidr!g" $kubeadm_init_yaml

    # hostname_ip="${master_hostname}_ip"
    # node_ip="${!hostname_ip}"
    sed -i "s/172.20.80.1/$apiserver_ip/g" $kubeadm_init_yaml
    sed -i "s!172.20.80.1!$k8s_master1_ip!g" $kubeadm_init_yaml
    sed -i "s!172.20.80.2!$k8s_master2_ip!g" $kubeadm_init_yaml
    sed -i "s!172.20.80.3!$k8s_master3_ip!g" $kubeadm_init_yaml
    
  
}

man(){
  # get kubeadm file.
  if [ $master_node_number = 1 ];then
    source_kubeadm_init_file="ipv4/kubeadm.init.onlyipv4.1.yaml"
    cp $source_kubeadm_init_file $kubeadm_init_yaml
    exit 1
  elif [ $master_node_number = 3 ];then
    source_kubeadm_init_file="ipv4/kubeadm.init.onlyipv4.3.yaml"
    cp $source_kubeadm_init_file $kubeadm_init_yaml
  elif [ $master_node_number = 5 ];then
    source_kubeadm_init_file="ipv4/kubeadm.init.onlyipv4.5.yaml"
    cp $source_kubeadm_init_file $kubeadm_init_yaml
    exit 1
  fi

}

man






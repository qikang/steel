#!/bin/bash
current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -x

etc_hosts="/etc/hosts"


for del_it in $k8s_all_hostname $k8s_all_ip;do
  sed -i "/$del_it/d" $etc_hosts
done

echo -e "$k8s_hosts_alias" >> $etc_hosts

# set registry and apiserver host alias.
registry_alias_addr="${registryServer_ip} $registryServer_alias"
sed -i "/$registry_alias_addr/d" $etc_hosts
echo $registry_alias_addr >> $etc_hosts

# set apiserver lb addr
apiserverLB_alias_addr="$apiserverLB_ip $apiserverLB_alias"
sed -i "/$apiserverLB_alias_addr/d" $etc_hosts
echo $apiserverLB_alias_addr >> $etc_hosts






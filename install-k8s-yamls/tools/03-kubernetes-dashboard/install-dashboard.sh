#!/bin/bash
current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -x

# create_dashboard_yaml
create_dashboard_yaml(){
  check_apiserver_ip_re=$(ip a | grep "$apiserverLB_ip" 2>/dev/null | grep -v grep || true)
  if [[ ! -z $check_apiserver_ip_re ]];then
    echo "hostname is `hostname`."
    
    # control_plane_nodes=$(kubectl get no -l node-role.kubernetes.io/control-plane= -o name | awk -F '/' '{print $2}')
    # for node in $control_plane_nodes;do
    #   kubectl taint node $node node-role.kubernetes.io/control-plane-
    #   kubectl taint node $node node.kubernetes.io/not-ready-
    # done

    dashboard_yaml="dashboard.v2.7.0.znlk.yaml"
    sed -i "s!ip-172-20-60-58!$traefikServer!g" $dashboard_yaml
    kubectl create -f $dashboard_yaml

  fi
}

api_server_check(){
  api_server_url="https://apiserver.cluster.local:6443/readyz?verbose"
  check_re=""
  until [[ ! -z $check_re ]];do
    sleep 5
    check_re=`curl -s -k $api_server_url | grep -o 'readyz check passed'`
  done
}

set_kube_apiserver_yaml(){

    # all master server
    cp token-auth-file.csv /etc/kubernetes/pki/
    sed -i '42i \ \ \ \ - --token-auth-file=/etc/kubernetes/pki/token-auth-file.csv' /etc/kubernetes/manifests/kube-apiserver.yaml
    api_server_check

    # kubectl -n kubernetes-dashboard get svc kubernetes-dashboard -o jsonpath='{.spec.ports[*].nodePort}'
    # echo "kubernetes-dashboard nodePort is: https://$apiserverLB_ip:30888"
    # echo "token: kUuBceAzp0nstmlvk1Z"

}


create_dashboard_yaml
set_kube_apiserver_yaml





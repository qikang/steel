#!/bin/bash
current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -ex

untaint_control_plane(){
  control_plane_nodes=$(kubectl get no -l node-role.kubernetes.io/control-plane= -o name | awk -F '/' '{print $2}')
  for node in $control_plane_nodes;do
    kubectl taint node $node node-role.kubernetes.io/control-plane-
  done
}


untaint_control_plane



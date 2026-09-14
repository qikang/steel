#!/bin/bash
current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -xe

traefik_chart="traefik-helm-chart-28.3.0.tar.gz"
tar -xf $traefik_chart

values_file="cloud.values.yaml"

kubectl create ns cloud

sed -i "s!ip-172-20-60-58!$traefikServer!g" $values_file
helm -n cloud install cross-traefik traefik-helm-chart-28.3.0/traefik -f $values_file 

# helm -n $ns install cross-traefik traefik/ \
#   --set image.registry=registry.local:5000 \
#   --set image.repository="k8s/traefik" \
#   --set image.tag="v3.0.2" \
#   --set ingressClass.enabled=true \
#   --set ingressClass.name=cloud \
#   --set ingressClass.isDefaultClass=true \
#   --set providers.kubernetesCRD.allowCrossNamespace=false \
#   --set providers.kubernetesCRD.allowExternalNameServices=true \
#   --set providers.kubernetesCRD.allowEmptyServices=true \
#   --set providers.kubernetesCRD.ingressClass=cloud \
#   --set providers.kubernetesCRD.namespaces={cloud} \
#   --set providers.kubernetesIngress.enabled="flase" \
#   --set ports.traefik.hostPort=9000 \
#   --set ports.web.hostPort=80 \
#   --set service.type=ClusterIP \
#   --set nodeSelector.kubernetes\\\.io/hostname=master1 \
#   --set tolerations[0].operator=Exists



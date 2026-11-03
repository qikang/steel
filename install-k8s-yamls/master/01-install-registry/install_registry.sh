#!/bin/bash
current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -x

pwd
ls -l

# images="images.k8s.tar.gz"
local_registry="/data/registry"

mkdir -p  $local_registry

# tar -xzf $images -C /data/registry


cp registry.service /etc/systemd/system/registry.service
cp config.yaml /data/registry/config.yaml
cp registry.$os_arch /usr/local/bin/registry


chmod 0755 /usr/local/bin/registry

systemctl daemon-reload
systemctl enable  registry.service
systemctl start registry.service
systemctl status registry.service --no-pager

echo "install registry service is done."

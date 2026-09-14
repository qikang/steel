#!/bin/bash
current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -ex

helm_file="helm-v3.13.1-linux-$os_arch.tar.gz"
helm_plugin_file="helm-plugin-push.dir.file.tar.gz"


# install helm
tar -xf $helm_file
mv linux-$os_arch/helm /usr/local/bin/helm
chmod +x /usr/local/bin/helm
rm -rf linux-$os_arch

# install helm push
tar -xf $helm_plugin_file -C /

helm plugin ls
helm env



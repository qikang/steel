#!/bin/bash
current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -ex

date_tag=$(date +"%F_%H_%M_%S")
dir_name="steel-archive.$date_tag"

# build steel file.
/bin/bash build-steel.sh

mkdir $dir_name

cp -r dist $dir_name/
cp -r install-k8s-yamls $dir_name/

tar -pzcf $dir_name.tar.gz $dir_name --remove-file


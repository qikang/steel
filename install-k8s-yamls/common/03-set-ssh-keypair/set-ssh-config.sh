#!/bin/bash
current_dir=$(cd `dirname $0`;pwd)
cd $current_dir
set -xe


config_file="/root/.ssh/config"
touch $config_file


echo 'Host *' > $config_file
echo 'StrictHostKeyChecking no' >> $config_file
echo 'ServerAliveInterval 60' >> $config_file
echo 'ServerAliveCountMax 10' >> $config_file
echo 'UserKnownHostsFile /dev/null' >> $config_file
echo '' >> $config_file
echo '' >> $config_file

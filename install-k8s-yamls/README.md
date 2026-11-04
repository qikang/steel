
## 前置条件
> **Prerequisite**: 
1. 所有节点操作系统软件源配置正确
2. 所有节点操作系统时区配置正确
3. 操作系统时间服务文件 install-k8s-yamls\common\04-install-chrony\master.chrony.conf 配置正确。
4. 提前下载 containerd 安装文件。<br>
   install-k8s-yamls/master/02-install-containerd/cri-containerd-cni-1.7.25-linux-amd64.tar.gz.download.url<br>
   install-k8s-yamls/master/02-install-containerd/cri-containerd-cni-1.7.25-linux-arm64.tar.gz.download.url<br>
5. 提前下载 kubernetes 相关文件，以下文件中有对应的url。<br>
   install-k8s-yamls/master/03-install-kubeadm_kubelet/install-kubelet.sh
7. 镜像下载 only amd64<br>
   swr.cn-north-4.myhuaweicloud.com/ddn-k8s/ghcr.io/flannel-io/flannel-cni-plugin:v1.7.1-flannel1<br>
   swr.cn-north-4.myhuaweicloud.com/ddn-k8s/ghcr.io/flannel-io/flannel:v0.27.3<br>
   swr.cn-north-4.myhuaweicloud.com/ddn-k8s/docker.io/kubernetesui/dashboard:v2.7.0<br>
   swr.cn-north-4.myhuaweicloud.com/ddn-k8s/docker.io/kubernetesui/metrics-scraper:v1.0.8
8. kubernetes 镜像仓库<br>
  registry.cn-hangzhou.aliyuncs.com/google_containers


---
## Kubernetes 部署架构
> **节点规划**
1. 1 个 master 节点
2. 1 个 master 节点，若干 worker 节点
3. 3 个 master 节点
4. 3 个 master 节点，若干 worker 节点

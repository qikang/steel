# Kubernetes 部署架构

本文档描述 Kubernetes 集群的部署架构方案,共提供两种部署模式以适应不同的可用性需求。

---

## 前置条件:需要设置 openEuler OS 软件源

> **Prerequisite**: openEuler OS software source must be configured

在开始 Kubernetes 集群部署之前,**必须**先完成 openEuler 操作系统的软件源配置。本项目使用本地 `172.20.60.86:5001` 上的 openEuler 24.03 LTS SP2 软件源,详细配置参见 [`common/set-local-repo/local_60.86.repo`](./common/set-local-repo/local_60.86.repo)。

## 方案一:三 Master 节点高可用架构

适用于生产环境,提供高可用的控制平面。包含 **3 个 Master 节点** 和若干 **Worker 节点**。

### Master 节点角色分工

| 节点 | 部署组件 | 说明 |
| :--- | :--- | :--- |
| **master1** | apiserver | API 服务器入口 |
| | controller-manager | 控制器管理器 |
| | scheduler | 调度器 |
| | etcd | 分布式存储(etcd 节点 1) |
| | traefik | Ingress 控制器 |
| | kubernetes-dashboard | Web 管理控制台 |
| **master2** | apiserver | API 服务器入口 |
| | controller-manager | 控制器管理器 |
| | scheduler | 调度器 |
| | etcd | 分布式存储(etcd 节点 2) |
| | StorageClass `nfs-client-provisioner` | NFS 动态存储供给 |
| | registry.local:5000 | 本地镜像仓库 |
| **master3** | apiserver | API 服务器入口 |
| | controller-manager | 控制器管理器 |
| | scheduler | 调度器 |
| | etcd | 分布式存储(etcd 节点 3) |
| | apiserver.cluster.local | 集群内部域名入口 |
| | chrony| 时间同步入口 |

### 架构特点

- **控制平面高可用**:API Server、Controller-Manager、Scheduler 在 3 个 Master 上同时运行,通过负载均衡对外提供服务。
- **etcd 集群**:3 节点 etcd 集群,容忍 1 个节点故障(`quorum = 2`)。
- **存储与入口分离**:镜像仓库、Ingress 控制器及存储供给统一部署在 master1/master2,职责清晰。

---

## 方案二:单 Master 节点架构

适用于开发、测试或资源受限环境。包含 **1 个 Master 节点** 和若干 **Worker 节点**。

### Master 节点角色分工

| 节点 | 部署组件 | 说明 |
| :--- | :--- | :--- |
| **master1** | apiserver | API 服务器入口 |
| | controller-manager | 控制器管理器 |
| | scheduler | 调度器 |
| | etcd | 分布式存储(单节点) |
| | registry.local:5000 | 本地镜像仓库 |
| | traefik | Ingress 控制器 |
| | kubernetes-dashboard | Web 管理控制台 |
| | StorageClass `nfs-client-provisioner` | NFS 动态存储供给 |
| | apiserver.cluster.local | 集群内部域名入口 |

### 架构特点

- **资源占用低**:所有控制平面组件集中部署在单一节点,适合资源受限场景。
- **部署简单**:无需额外的负载均衡与 etcd 集群配置,搭建速度快。
- **无单点冗余**:Master 节点故障将导致整个集群控制平面不可用,仅建议用于非生产环境。

---

## 方案对比

| 维度 | 三 Master 高可用 | 单 Master |
| :--- | :--- | :--- |
| 可用性 | 高(容忍 Master 故障) | 低(单点故障) |
| 资源需求 | 高(3 台 Master) | 低(1 台 Master) |
| etcd 集群 | 3 节点 | 单节点 |
| 适用场景 | 生产环境 | 开发 / 测试 |
| 部署复杂度 | 较高 | 较低 |

---

## 通用说明

- **Worker 节点**:两种方案均包含若干 Worker 节点,用于运行业务 Pod。
- **组件镜像**:所有镜像优先从 `registry.local:5000` 本地仓库拉取,降低外网依赖。
- **网络入口**:通过 `traefik` 统一对外提供 HTTP/HTTPS 入口。
- **存储供给**:通过 `nfs-client-provisioner` 实现 PVC 的动态创建与绑定。

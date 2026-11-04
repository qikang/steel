# Steel 使用说明

`steel` 是单二进制、远端无 agent 的轻量编排工具，面向 K8s 装机场景。本文覆盖三件事：**主机清单怎么写**、**环境变量怎么生成**、**安装流程怎么跑**。

> 本文以仓库内 `install-k8s-yamls/` 的实际文件为准，脚本与清单名均可直接对照。

---

## 1. 快速开始

```bash
go build -o steel .                                          # 编译
steel ping  install-k8s-yamls/3master_1node.k8s.yaml         # 测连通性
steel apply install-k8s-yamls/3master_1node.k8s.yaml         # 执行安装
```

仓库自带 4 份入口清单，按拓扑选一份改：

| 文件 | 拓扑 |
| --- | --- |
| `install-k8s-yamls/1master.k8s.yaml` | 1 master |
| `install-k8s-yamls/1master_2node.k8s.yaml` | 1 master + 2 worker |
| `install-k8s-yamls/3master.k8s.yaml` | 3 master + 1 worker |
| `install-k8s-yamls/3master_1node.k8s.yaml` | 3 master + 1 worker |

常用参数：

| 参数 | 说明 |
| --- | --- |
| `--skip-probe` | 跳过 apply 阶段的连通性探测 |
| `--probe-timeout 10s` | 单主机探测超时（默认 3s） |

apply 启动时会打印组信息 + 4 条人工确认提示（`/data` 挂载 / DNS / 时区 / OS 软件源），回车或 `Y` 继续，`N` 退出。**这 4 条 steel 只打印、不探测**，需人工确认。

---

## 2. 主机清单（inventory）

### 2.1 结构

steel 没有独立的 `hosts.yaml`，所有主机信息写在**入口 Playbook 顶层 `hosts:` 块**里，且只能是 keyed-map 形态：

```yaml
hosts:
  masters:                       # 组名
    k8s_master1:                # hosts-map key → 决定 k8s_master1_ip 这类名字
      hostname: ip-192-168-170-48   # SSH 实际连谁
      ip: 192.168.170.48
      user: root
      password: your_password   # 与 ssh_key 二选一
      port: 22                  # 可选，默认 22
      plugin:                    # 可选，角色标记，见 2.4
        registryServer:
          alias: registry.local
  workers: {}                   # 空组合法
  addworkers: {}                # 扩容专用，可选，见 2.5
```

> map key 与 `hostname:` 是两个独立字段：key 决定内置变量名，`hostname` 决定 SSH 目标。

### 2.2 主机字段

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `hostname`（`name`） | ✅ | 主机名，SSH 连谁 |
| `ip`（`hostip`） | ✅ | IP 或可解析域名 |
| `user`（`username`） | ✅ | SSH 登录用户 |
| `ssh_key`（`sshkey`） | ✅\* | 私钥路径，`~` 自动展开 |
| `password` | ✅\* | 密码 |
| `port` | ❌ | SSH 端口，默认 22 |
| `plugin` | ❌ | 角色标记，见 2.4 |

- `ssh_key` 与 `password` **至少填一个**；都填优先 `ssh_key`；都不填且存在 `SSH_AUTH_SOCK` 时回落 ssh-agent。加密私钥（passphrase）当前不支持，会直接报错。
- 加载期硬校验：`hostname` / `ip` / `user` 缺失 → 报错；同一 `hostname` 重复声明 → 报错；`masters` 组数量不是 1/3/5 → 报错。

### 2.3 分组语义

| 写法 | 含义 |
| --- | --- |
| `hosts: { masters: {...} }` | 声明一组**主机**，组名 `masters` |
| 顶层 `masters: [k8s_master1, k8s_master2]` | 声明一个**组成员列表**，成员是 hostname 或其他组名 |

- `hosts:` 下的每个 key 自动成为隐式组，Play 里 `hosts: masters` 即可命中。
- 顶层 list-of-strings 是 `hosts:` 的**兄弟字段**，用于组合自定义组：`allnode: [masters, workers]`、`etcd: [masters]`、`lb: [master1]`。
- 组循环（A → B → A）、引用不存在的成员、同名组在两处重复声明，加载期直接拒。

**`allnode` 是内置组**（代表"参与初始 bootstrap 的节点"）：

- 未显式声明时，steel 自动构建 = `hosts:` 下所有组按字典序展开拼接，**排除 `addworkers`**。
- 显式声明时以用户为准（如 `allnode: [masters, workers, addworkers]`）。
- 生产建议不要显式声明，让 `addworkers` 天然隔离在 bootstrap 之外。

### 2.4 plugin 角色标记

`plugin:` 声明"这台主机承担什么角色"，steel 据此派生出远端脚本可直接使用的环境变量：

```yaml
plugin:
  traefikServer: true                              # 布尔形态
  registryServer: { alias: registry.local }        # map 形态
```

| 写法 | 派生出的环境变量 |
| --- | --- |
| `traefikServer: true` | `traefikServer=<hostName>`、`traefikServer_ip=<IP>` |
| `registryServer: { alias: x }` | `registryServer_hostName=<hostName>`、`registryServer_ip=<IP>`、`registryServer_alias=x` |

- 一台主机可同时声明多个角色，各自独立派生。
- 派生变量注入全局 vars，**每台主机都能读到**，值指向声明该角色的那台主机。
- 两个不同主机声明同一角色时，后遍历到的覆盖前者（不报错），请自行保证角色唯一。

`install-k8s-yamls` 实际用到的角色：

| 角色 | 消费方 |
| --- | --- |
| `registryServer` | `common/01-etc-hosts/set-etc-hosts.sh` 写 registry 别名；`master/01-install-registry/` 在该主机装 registry |
| `apiserverLB` | `set-etc-hosts.sh` 写 apiserver 别名；`master/03-install-kubeadm_kubelet/` 在该主机执行 `init-master.sh` |
| `timeServer` | `common/04-install-chrony/install-chrony.sh` 据此选 `master.chrony.conf`（服务端）或 `node.chrony.conf`（客户端） |
| `nfsServer` | 预留（`tools/` 下暂无 nfs 安装脚本） |
| `traefikServer` | 预留（`tools/` 下暂无 traefik 安装脚本） |

### 2.5 addworkers（扩容隔离）

`addworkers` 是顶层 `hosts:` 的兄弟字段，形态与 `masters` / `workers` 完全一致（keyed-map，不是列表）：

```yaml
addworkers:
  k8s_worker1:
    hostname: node1
    ip: 192.168.170.22
    user: root
    password: your_password
```

它的隔离语义（在 inventory 加载期就生效）：

- 不进入自动构建的 `allnode`
- 不进入 `k8s_all_ip` / `k8s_all_hostname` 聚合变量 → etcd `--initial-cluster`、kubelet peer list 不会带上它们
- Play 必须显式写 `hosts: addworkers` 才会命中，不会被隐式 `allnode` 误带跑

> 该字段是**可选**的：`1master.k8s.yaml` / `3master.k8s.yaml` 带了 `addworkers: {}` 占位，另两份没带，两种都合法。

### 2.6 完整示例

以 `install-k8s-yamls/3master_1node.k8s.yaml` 为准：

```yaml
hosts:
  masters:
    k8s_master1:
      hostname: ip-192-168-170-48
      ip: 192.168.170.48
      user: root
      password: your_password
      port: 22
      plugin:
        traefikServer: true
    k8s_master2:
      hostname: ip-192-168-170-49
      ip: 192.168.170.49
      user: root
      password: your_password
      port: 22
      plugin:
        registryServer:
          alias: registry.local
        nfsServer: true
    k8s_master3:
      hostname: ip-192-168-170-47
      ip: 192.168.170.47
      user: root
      password: your_password
      port: 22
      plugin:
        apiserverLB:
          alias: apiserver.cluster.local
        timeServer: true
  workers:
    k8s_worker1:
      hostname: node1
      ip: 192.168.170.22
      user: root
      password: your_password

vars:
  pod_network_cidr: 10.244.0.0/16
  service_cidr: 10.96.0.0/12
  dns_domain: cluster.local
  data_root: /data/install-k8s-remoteDirectory
  k8s_network_stack: ipv4

runlist:
  - test-env.yaml
  - common/os-init.yaml
  - master/master.yaml
  - tools/tools.yaml
```

---

## 3. vars 与模板

### 3.1 vars 块

入口 YAML 顶层 `vars:` 定义全局参数，所有 Play 共享；支持嵌套 map，字段用 `.` 访问：

```yaml
vars:
  pod_network_cidr: 10.244.0.0/16
  registryServer:
    alias: registry.local
    hostName: master2      # 这台主机是谁
```

### 3.2 `{{ }}` 查找顺序

`{{ }}` 在 `shell.cmd/chdir/bash`、`copy.src/dest`、`hosts`、`name`、runlist 子文件名处都会渲染。

裸名（不带 `.`）按顺序查找：

1. **vars 顶层** —— 用户声明优先，可覆盖内置同名项
2. **host builtins** —— `hostname` / `hostip` / `username` / `password` / `sshkey` / `os_arch` / `master_node_number`
3. **inventory builtins** —— `all_<group>_hostname`，组名单数形式（`all_master_hostname` 命中 `masters` 组），空格分隔 hostname 列表；`allnode` 不暴露

带 `.` 的占位符：`host.<field>` 走当前主机事实；其他首段走 vars map 逐层 walk。

未知占位符立即报错：`placeholder "xxx" not found`。

### 3.3 hosts 字段用模板（动态目标）

`install-k8s-yamls` 里就是这么定位"某台特定角色主机"的：

```yaml
- name: install registry
  hosts: {{ registryServer.hostName }}     # → ip-192-168-170-49
  shell:
    cmd: /bin/bash install_registry.sh
    chdir: "{{ data_root }}/master/01-install-registry"
```

流程：模板渲染成字符串 → 交给 inventory 解析 → 单主机名得到 1 个 target，组名展开该组全部成员（递归展开 + 去重）。

> ⚠️ `hosts:` 渲染时 host context 还没确定，**不能用** `{{ hostname }}` / `{{ host.ip }}` 这类 per-host 变量，只能引用 vars 或 inventory builtins。

---

## 4. 环境变量生成规则

### 4.1 机制：prelude

每个 `shell` 模块执行前，steel 拼一段 `export k=v; export k2=v2; ...;` 前置脚本，再以 `bash -l -c "<prelude>; <用户命令>"` 在远端登录 shell 执行。所有 export 的变量被子进程继承，远端脚本里直接 `$VAR` 引用。

### 4.2 注入顺序（固定）

| # | 变量 | 来源 | 用途 |
| --- | --- | --- | --- |
| 1 | `os_arch` | 首连时 SSH `uname -m`，`x86_64→amd64`、`aarch64→arm64`；按 host.Name 缓存 | 远端按架构选二进制 |
| 2 | `master_node_number` | `masters` 组数量（1/3/5） | etcd quorum / kubeadm 拓扑判断 |
| 3 | `k8s_master1_ip` | hosts-map key 归一化为 `k8s_master1` 的那台主机 IP | bootstrap 第一个 master 地址 |
| 4 | `k8s_hosts_alias` | 全清单 `ip hostname` 对，分隔符是字面量 `\n ` | `echo -e "$k8s_hosts_alias" >> /etc/hosts` |
| 5 | 其余变量 | 派生后的 vars（标量直出，嵌套 map 用 `_` 拍平） | 用户参数 |

- 1–4 逐主机注入；5 是全局共享。
- 其他架构（`ppc64le` / `riscv64`）在探测阶段直接 fatal。
- **只有 `k8s_master1_ip` 会被导出**，`k8s_master2_ip` / `k8s_master3_ip` 等不会（`generate_config_ipv6.sh`、`generate_config_dual-stack.sh` 里引用的那两个变量取不到值，这两个脚本当前也不通，见 8）。

### 4.3 自动派生规则

派生在 Go 进程内、prelude 之前按固定顺序完成：

| 顺序 | 规则 | 触发条件 | 产出 |
| --- | --- | --- | --- |
| 1 | plugin 派生 | 主机 `plugin:` 字段 | 布尔 → `<key>: <hostName>`；map → `<key>: {hostName, ip, ...原字段}` |
| 2 | 嵌套 map 注入 `ip` | 任意层级 map 含 `hostName: <主机名>` | 同 map 兄弟字段 `ip: <IP>` |
| 3 | 顶层字符串注入 `_ip` | 顶层 `vars.<key>: <主机名>` | `<key>_ip: <IP>` |
| 4 | `noapiserverips` | `vars.apiserverLB.hostName` 命中某 master | 其余 master IP，空格分隔 |
| 5 | `k8s_all_ip` | 未显式声明 | 初始节点 IP，空格分隔（**排除 addworkers**） |
| 6 | `k8s_all_hostname` | 未显式声明 | 初始节点 hostname，空格分隔（**排除 addworkers**） |

- 规则 2、3 只在 `ip` / `<key>_ip` 未显式声明时注入；主机名解析不到时静默跳过（不写空值，避免掩盖拼写错误）。
- **用户显式声明一律优先** —— 上面每一项都不会覆盖 `vars:` 里已存在的同名 key。

### 4.4 拍平与编码

- 嵌套 map 递归拍平，父子段用 `_` 连接：`registryServer.hostName` → `registryServer_hostName`
- 所有值单引号包裹（空格 / 单引号 / 反斜杠都会转义）
- map 的 key 按字典序导出，字节稳定，便于日志 diff
- 数组 / 未知类型 fallback `fmt.Sprintf("%v")`，不静默丢字段
- prelude 与用户命令之间用 `; ` 隔开（空格会让首个 token 被拼进上一个 export）

### 4.5 变量在脚本里的实际消费

这是校验规则是否真的落地的地方——每个注入的变量都有对应消费方：

| 变量 | 消费脚本 | 用途 |
| --- | --- | --- |
| `$os_arch` | `install-kubelet.sh`、`install-containerd.sh`、`install_registry.sh` | 选 `$os_arch` 架构的包 / 二进制 |
| `$master_node_number` | `generate_config_ipv4.sh` | 选 `ipv4/kubeadm.init.onlyipv4.{1,3,5}.yaml` |
| `$apiserverLB_ip` | `init-master.sh`、`join-k8s-on-the-master1.sh` | apiserver 地址 |
| `$apiserverLB_alias` | `set-etc-hosts.sh` | apiserver 别名写入 /etc/hosts |
| `$noapiserverips` | `generate_config_ipv4.sh` | 其余 master IP，替换 etcd endpoint 占位 IP |
| `$pod_network_cidr` / `$service_cidr` | `generate_config_ipv4.sh` | 替换 kubeadm 配置里的网段 |
| `$k8s_network_stack` | `init-master.sh` | 选择配置生成脚本（当前只支持 ipv4） |
| `$k8s_all_ip` / `$k8s_all_hostname` | `set-etc-hosts.sh` | 清理 /etc/hosts 旧记录 |
| `$k8s_hosts_alias` | `set-etc-hosts.sh` | 整张主机表写入 /etc/hosts |
| `$registryServer_ip` / `$registryServer_alias` | `set-etc-hosts.sh` | registry 别名写入 /etc/hosts |
| `$timeServer` | `install-chrony.sh` | 判断本机是 ntp 服务端还是客户端 |

### 4.6 实际导出样例

`install-k8s-yamls/test-env.yaml` 就是现成的探针——它对 `allnode` 跑 `env`，且是 runlist 的第一项：

```text
data_root=/data/install-k8s-remoteDirectory
k8s_master1_ip=192.168.170.48
k8s_hosts_alias=192.168.170.48 ip-192-168-170-48\n 192.168.170.49 ip-192-168-170-49\n 192.168.170.47 ip-192-168-170-47\n 192.168.170.22 node1
k8s_all_ip=192.168.170.48 192.168.170.49 192.168.170.47 192.168.170.22
k8s_all_hostname=ip-192-168-170-48 ip-192-168-170-49 ip-192-168-170-47 node1
pod_network_cidr=10.244.0.0/16
service_cidr=10.96.0.0/12
dns_domain=cluster.local
k8s_network_stack=ipv4
traefikServer=ip-192-168-170-48
traefikServer_ip=192.168.170.48
nfsServer=ip-192-168-170-49
nfsServer_ip=192.168.170.49
timeServer=ip-192-168-170-47
timeServer_ip=192.168.170.47
apiserverLB_hostName=ip-192-168-170-47
apiserverLB_alias=apiserver.cluster.local
apiserverLB_ip=192.168.170.47
registryServer_hostName=ip-192-168-170-49
registryServer_alias=registry.local
registryServer_ip=192.168.170.49
os_arch=amd64
master_node_number=3
noapiserverips=192.168.170.48 192.168.170.49
```

> `k8s_hosts_alias` 里的 `\n` 是**字面量**（导出时被单引号包住，不会被 shell 展开），`echo -e` 才会把它变成真实换行——所以 `set-etc-hosts.sh` 用的是 `echo -e "$k8s_hosts_alias" >> /etc/hosts`。

---

## 5. Play 执行与装机流程

### 5.1 执行模式

- **Play 之间**顺序执行，前一个 Play 全部主机成功才进入下一个。
- **Play 内部**默认并发，任一主机失败 → fail-fast，整个 apply 终止。
- 顺序敏感的场景（kubeadm join）加 `execution_mode: serial`：

```yaml
- name: other masters join k8s cluster
  hosts: masters
  shell:
    cmd: /bin/bash join-k8s-on-the-master1.sh control-plane
    chdir: "{{ data_root }}/master/03-install-kubeadm_kubelet"
    bash: /bin/bash
  execution_mode: serial
```

只有字面量 `serial` 触发串行，其他值（含拼错）都走默认并发。

### 5.2 顶层 runlist

```yaml
runlist:
  - test-env.yaml        # 对 allnode 跑 env，打印注入的环境变量
  - common/os-init.yaml  # 全部节点初始化
  - master/master.yaml   # registry → containerd → kubeadm/kubelet + 集群初始化
  - tools/tools.yaml     # CNI → Dashboard
```

### 5.3 common 阶段（`common/os-init.yaml`，全部节点）

| 子 playbook | 做什么 |
| --- | --- |
| `01-etc-hosts/set-etc-hosts.yaml` | 建 `{{ data_root }}` 目录、分发脚本、写 /etc/hosts、设时区 `Asia/Shanghai`、`hostnamectl set-hostname {{ hostname }}` |
| `02-set-local-repo/set-os-repo.yaml` | 切离线 / 内网软件源（`local_60.86.repo`） |
| `03-set-ssh-keypair/install-keypair.yaml` | 分发 `id_rsa` / `id_rsa.pub` 到 `/root/.ssh`，配 `authorized_keys` 实现节点互信 |
| `04-install-chrony/install-chrony.yaml` | 装 chrony，按 `$timeServer` 选 master / node 配置 |
| `05-set-os-kernel/set-kernel.yaml` | 内核参数调优（`sysctl.k8s.conf`、`modules.k8s.conf`） |

### 5.4 master 阶段（`master/master.yaml`）

| 子 playbook | 做什么 |
| --- | --- |
| `01-install-registry/install-registry.yaml` | 仅在 `{{ registryServer.hostName }}` 上装 Docker Registry |
| `02-install-containerd/install-containerd.yaml` | 全部节点装 containerd |
| `03-install-kubeadm_kubelet/install-kubelet.yaml` | 全部节点装 kubelet/kubeadm/kubectl；随后 `{{ apiserverLB.hostName }}` 跑 `init-master.sh`；其余 master 与 worker 以 `serial` 逐个 `join-k8s-on-the-master1.sh`；最后 `untaint_control_plane.sh` |

`init-master.sh` 的流程：`k8s_network_stack` 决定用哪个 `generate_config_*.sh`（当前只有 ipv4 可用）→ 按 `$master_node_number` 选 kubeadm init 模板并替换网段与 apiserver 地址 → `kubeadm config images pull` → `kubeadm init --upload-certs` → 写 `~/.kube/config`。

### 5.5 tools 阶段（`tools/tools.yaml`）

1. 把 `install-k8s-yamls/tools` 复制到所有 master 的 `{{ data_root }}/tools`
2. 在 `{{ apiserverLB.hostName }}` 上执行 `01-install-CNI/install-cni.sh`（`kubectl create -f flannel/kube-flannel.yaml`，等待 vxlan 网卡 `flannel.1` 出现）
3. 在 masters 上以 `serial` 执行 `02-kubernetes-dashboard/install-dashboard.sh`（dashboard v2.7.0）
4. 回显 `Installation Complete`

> 当前 `tools/` 下只有 `01-install-CNI/`（flannel）和 `02-kubernetes-dashboard/`。Helm、Traefik、NFS provisioner、debug 工具**不在仓库内**，需要自行补充脚本与目录。

### 5.6 扩容（addworkers）

1. 在入口清单顶层 `addworkers:` 填入新节点（见 2.5）
2. 把 runlist 改成只跑扩容：`addworkers/add-node.yaml`
3. 重新 `steel apply <入口清单>`

`addworkers/add-node.yaml` 的 runlist 是独立的一套（不复用 `common/`）：

```yaml
runlist:
  - 01-set-etc-hosts.yaml
  - 02-set-os-repo.yaml
  - 03-install-keypair.yaml
  - 04-install-chrony.yaml
  - 05-set-kernel.yaml
  - 06-install-containerd.yaml
  - 07-install-kubelet.yaml
```

`07-install-kubelet.yaml` 复制 kubeadm 脚本后以 `serial` 执行 `join-k8s-on-the-master1.sh worker` 完成 join。因为 `addworkers` 在加载期就与 `allnode` / 聚合变量隔离，扩容不会污染原集群的 bootstrap 数据。

---

## 6. 日志与排障

每次 apply 生成 `logs/<UTC-时间戳>/run.log`，全流程按执行顺序追加，行内带 `host=` / `play=` 标记：

```bash
grep '^host=master1 ' logs/<ts>/run.log                 # 某台主机全部输出
grep 'play="01-etc-hosts:set-etc-hosts"' logs/<ts>/run.log   # 某个 play
grep -E 'exit=[1-9]|stderr' logs/<ts>/run.log           # 失败节点
```

---

## 7. 常见错误速查

| 现象 | 原因 | 修正 |
| --- | --- | --- |
| `hosts[masters] must be a mapping` | 组用了 list 形态 | 改为 keyed-map |
| `host "X" is missing 'ip'` 等 | 必填字段缺失 | hostname / ip / user 都填 |
| `host "X" needs either 'ssh_key'… or 'password'` | 凭据都没给 | 至少填一个，或走 ssh-agent |
| `masters group has N hosts; steel supports only 1, 3, or 5` | master 数量不合法 | 改为 1 / 3 / 5 |
| `group "X" references unknown member "Y"` | 组引用了不存在的成员 | 成员必须是某主机 hostname 或已声明组 |
| `group cycle involving "X"` | 组循环引用 | 拆掉其中一个引用 |
| `placeholder "xxx" not found` | 模板变量未声明 | 在 `vars:` 声明或改用内置名 |
| `hosts:` 里用 `{{ hostname }}` 渲染失败 | 该字段渲染时无 host context | 改用 vars 引用或 `{{ all_master_hostname }}` |
| `$nfsServer_ip` 为空 | vars 里主机名拼错，派生静默跳过 | 校对 `hostname:` |
| `chdir: {{ data_root }}` YAML 解析异常 | 未加引号被当成 flow mapping | 整个值用双引号包起 |
| `no support ipv6` / `no support dual-stack` | `k8s_network_stack` 只实现了 ipv4 | 改 `k8s_network_stack: ipv4` |

---

## 8. 注意事项

- **不校验主机指纹**（`InsecureIgnoreHostKey`），生产环境靠堡垒机 / known_hosts / VPN 做外层隔离。
- **网络栈只支持 ipv4**：`init-master.sh` 对 `ipv6` / `dual-stack` 直接 `exit 1`。`generate_config_ipv6.sh` / `generate_config_dual-stack.sh` 虽在仓库里，但未被调用，且它们引用的 `$apiserver_ip`、`$k8s_master2_ip`、`$k8s_master3_ip` 都**不是** steel 会注入的变量（prelude 只导出 `k8s_master1_ip`）。
- **kubeadm 用的是官方 openEuler EPOL RPM**（`kubeadm-1.29.5-150500.1.1.*.rpm`，证书默认 1 年）。`install-kubelet.sh` 里有一段把证书有效期改成 100 年的逻辑，但**当前是注释状态**；若要启用需自行提供 `kubeadm-1.29.5-$os_arch-100year` 二进制并自行处理 `kubeadm certs renew`。
- **不要提交敏感信息**：`common/03-set-ssh-keypair/id_rsa*`、playbook 里的明文 `password:`、`logs/`（`.gitignore` 已排除 `**id_rsa`、`**id_rsa.pub`、`logs`、`dist`、`*.rpm`、`*.gz`）。
- **二进制 / 离线包不随仓库提交**（`*.rpm`、`*.gz` 被 gitignore），本地必须提前准备好：

| 路径 | 用途 | 获取方式 |
| --- | --- | --- |
| `master/02-install-containerd/cri-containerd-cni-1.7.25-linux-{amd64,arm64}.tar.gz` | containerd + CNI + cri | 同目录 `.download.url` 文件里是官方 release URL |
| `master/02-install-containerd/containerd.etc.tar.gz` | containerd 的 systemd 配置 | 随包分发 |
| `master/03-install-kubeadm_kubelet/rpms/{amd64,arm64}/*.rpm` | k8s 1.29.5 + cri-tools | `install-kubelet.sh` 里有注释掉的 wget 命令（mirrors.aliyun.com/kubernetes-new） |
| `master/01-install-registry/registry.{amd64,arm64}` | Docker Registry 二进制 | <https://github.com/distribution/distribution/releases> |
| `master/01-install-registry/registry.k8s.images.repo` | 待推入本地 registry 的镜像清单 | 清单文件在仓库内，镜像本身需从 `registry.cn-hangzhou.aliyuncs.com` 拉取 |

`install-k8s-yamls/README.md` 里另有前置条件清单（软件源、时区、chrony 配置、flannel / dashboard 镜像地址），装机前请对照检查。

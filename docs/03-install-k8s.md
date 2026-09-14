# K8s 安装方法(基于 Steel + install-k8s-yamls)

> 本节讲如何用 Steel 把 [`install-k8s-yamls/`](../install-k8s-yamls/) 跑起来,完成一个生产形态的 K8s 集群安装,以及后续的扩容操作。

完整源码参考:
- [`cmd/apply.go`](../cmd/apply.go) —— apply 主流程(预检 → 连通性探测 → 派发)
- [`internal/runner/apply.go`](../internal/runner/apply.go) / [`play.go`](../internal/runner/play.go) —— play 编排
- [`internal/playbook/playbook.go`](../internal/playbook/playbook.go) —— runlist 解析与 play 名拼接

---

## 1. 架构与适用场景

Steel 的设计目标:**单二进制、远端无 agent、聚焦 K8s 装机场景的轻量编排**。具体场景:

- ✅ 多 master 集群(1/3/5)的 bootstrap
- ✅ worker 节点批量加 join
- ✅ 集群初始化后的扩容(添加新 master / worker)
- ✅ 内置 Docker Registry / NFS / Traefik / Dashboard / Helm 等 K8s 生态组件的离线安装
- ❌ 不适合:通用配置管理(Ansible 那种几千模块的体系)、Windows 主机、TLS 证书自动签发(本仓库内置的 kubeadm 二进制已 patch 100 年证书,见 §9)

支持的 CPU 架构(参考 [`internal/runner/arch/arch.go`](../internal/runner/arch/arch.go)):

- ✅ `x86_64` → 远端拿到 `os_arch=amd64`
- ✅ `aarch64` → 远端拿到 `os_arch=arm64`
- ❌ `ppc64le` / `riscv64` / 其他 —— 架构探测阶段直接 fatal

支持的 `masters` 规模(强约束,见 [`internal/inventory/validate.go`](../internal/inventory/validate.go)):

- ✅ 单 master(开发测试)
- ✅ 3 master HA(标准生产)
- ✅ 5 master HA(更大规模)
- ❌ 其他规模(2/4/7/9 等)——加载期直接拒

---

## 2. 安装前准备

### 2.1 编译 steel

```bash
git clone https://github.com/qikang/steel.git
cd steel
go build -o steel .
```

产物是单一二进制,`scp` 到任意一台带 SSH 客户端的机器即可使用。

### 2.2 准备 SSH 凭据

任意一种:

```yaml
# 方案 A:私钥
ssh_key: ~/.ssh/id_rsa

# 方案 B:密码(password 字段)
password: your_password

# 方案 C:留空,回落本地 ssh-agent(SSH_AUTH_SOCK 存在时)
```

⚠️ **不校验主机指纹**(`InsecureIgnoreHostKey`)——生产环境必须靠堡垒机 / known_hosts / VPN 等外层网络隔离。

### 2.3 准备目标机器

**关键前置条件**(apply 启动前的 4 条提示,见 [cmd/apply.go](../cmd/apply.go)):

1. `/data` 目录与 data 盘正确挂载(对应 `vars.data_root: /data/install-k8s-remoteDirectory`)
2. `/etc/resolv.conf` DNS 解析正常
3. 时区设置正确(脚本会强制 `Asia/Shanghai`,见 `common/01-etc-hosts/set-etc-hosts.yaml`)
4. OS 软件源设置正确

> ⚠️ Steel **不实际探测**这 4 条,binary 只打印提示,操作员自行确认。回答 `Y` 或直接回车(空行默认 yes)继续,`N` 立即退出。

### 2.4 编辑入口 YAML

最小可装机入口:[`install-k8s-yamls/install-k8s.yaml`](../install-k8s-yamls/install-k8s.yaml)。重点修改:

```yaml
hosts:
  masters:
    k8s_master1:
      hostname: master1                # ← 改成你的主机名
      ip: 192.168.170.48              # ← 改成实际 IP
      user: root
      password: <你的密码>             # ← 必须改;或换成 ssh_key
      plugin:
        traefikServer: true
        registryServer: { alias: registry.local }
        nfsServer: true
        apiserverLB: { alias: apiserver.cluster.local }
        timeServer: true
    # k8s_master2 / k8s_master3 同上(3 HA 时)
  workers: {}                          # 空 / 填 worker 主机
  addworkers: {}                       # 装机阶段保持空

vars:
  pod_network_cidr: 10.244.0.0/16
  service_cidr: 10.96.0.0/12
  dns_domain: cluster.local
  data_root: /data/install-k8s-remoteDirectory
  k8s_network_stack: ipv4              # ipv4 / ipv6 / dual-stack
```

也可以直接复用仓库自带的模板:

- [`install-k8s-yamls/1master-k8s.yaml`](../install-k8s-yamls/1master-k8s.yaml) —— 单 master 最小化
- [`install-k8s-yamls/3master_1node-k8s.yaml`](../install-k8s-yamls/3master_1node-k8s.yaml) —— 3 HA + 1 worker
- [`install-k8s-yamls/1master_2node-k8s.yaml`](../install-k8s-yamls/1master_2node-k8s.yaml) —— 单 master + 2 worker

---

## 3. 执行流程(端到端)

### 3.1 第一步:测试连通性

```bash
steel ping install-k8s-yamls/install-k8s.yaml
```

这一步会 SSH 登录每台主机跑 `echo pong` 并校验输出,**这是验证网络 / 凭据 / 命令执行链路最快的方法**。退出码 0 表示全部可达,非零即有失败。

> 也可以指定具体目标(主机名 / 组名):
> ```bash
> steel ping install-k8s-yamls/install-k8s.yaml masters
> steel ping install-k8s-yamls/install-k8s.yaml master1
> ```

### 3.2 第二步:执行安装

```bash
steel apply install-k8s-yamls/install-k8s.yaml
```

执行过程中你会看到:

1. **打印组信息**:`masters / workers / allnode / addworkers` 各组的主机数
2. **打印 4 条自检提示**:`/data` 挂载 / DNS / 时区 / OS 软件源
3. **Y/N 询问**:回 `Y` 或直接回车继续,`N` 立即退出
4. **连通性探测**:对每台主机 SSH 跑 `echo pong` + 输出校验,任一不可达即终止整个 apply
5. **按 runlist 顺序派发 play**:每个 play 默认并发,play 之间顺序
6. **日志写入**:`logs/<UTC-时间戳>/run.log`,所有 play / 所有 host 的输出按执行顺序追加,带 `host=` / `play=` 标记

### 3.3 常用参数

```bash
# 跳过连通性探测(适用 CI / 已用 ping 验证过的场景)
steel apply --skip-probe install-k8s-yamls/install-k8s.yaml

# 调整单主机探测超时(默认 3s,网络较慢时可拉长)
steel apply --probe-timeout 10s install-k8s-yamls/install-k8s.yaml

# 两个 flag 可同时使用
steel apply --skip-probe --probe-timeout 30s install-k8s-yamls/install-k8s.yaml
```

---

## 4. install-k8s-yamls 目录结构与执行流程

```
install-k8s-yamls/
├── install-k8s.yaml          # 入口文件(顶层 runlist)
├── 1master-k8s.yaml          # 单 master 模板
├── 1master_2node-k8s.yaml    # 1 master + 2 worker 模板
├── 3master_1node-k8s.yaml    # 3 HA + 1 worker 模板
├── test-env.yaml             # 测试远端 shell 环境变量(开发期用)
├── common/                   # 所有节点的初始化(os-init.yaml 是其入口)
│   ├── os-init.yaml
│   ├── 01-etc-hosts/         # /etc/hosts + 时区 + hostname
│   ├── 02-set-local-repo/    # 离线软件源
│   ├── 03-set-ssh-keypair/   # 互信(放 id_rsa / id_rsa.pub)
│   ├── 04-install-chrony/    # NTP 时间同步
│   └── 05-set-os-kernel/     # 内核参数(sysctl)
├── master/                   # master 节点专属组件
│   ├── master.yaml           # master 入口 runlist
│   ├── 01-install-registry/  # Docker Registry(registryServer 主机)
│   ├── 02-install-containerd/# containerd 安装(全节点)
│   └── 03-install-kubeadm_kubelet/  # kubeadm + kubelet + 集群初始化
├── tools/                    # K8s 生态组件(全在 apiserverLB 节点)
│   ├── tools.yaml            # tools 入口 runlist
│   ├── 01-install-CNI/       # Calico / Flannel 等
│   ├── 02-helm/              # Helm CLI
│   ├── 03-kubernetes-dashboard/
│   ├── 04-traefik/           # Ingress Controller
│   ├── 05-nfs-sc/            # NFS subdir external provisioner
│   └── 06-debug/             # 排障工具
└── addworkers/               # 扩容节点专用
    ├── add-node.yaml         # 扩容入口 runlist
    └── 01-07 ...             # 子 playbook(独立于 common)
```

### 4.1 顶层入口的 runlist 顺序

```yaml
runlist:
  - test-env.yaml             # 1. 测试远端环境变量连通性(开发期)
  - common/os-init.yaml       # 2. 所有节点初始化(os-init 内部又串了 5 步)
  - master/master.yaml        # 3. master 专属(registry / containerd / kubeadm 引导)
  - tools/tools.yaml          # 4. K8s 生态组件
```

> 装机完成后想扩容:把 `addworkers:` 字段填上,在 `install-k8s.yaml` 把 runlist 改成 `addworkers/add-node.yaml` 单跑(详见 §7)。

### 4.2 common 阶段(全节点)

`common/os-init.yaml` 串行执行 5 个步骤:

1. **`01-etc-hosts`** —— 把所有节点的 `ip hostname` 对写入 `/etc/hosts`,设置时区 `Asia/Shanghai`,设置 hostname
2. **`02-set-local-repo`** —— 切换到离线 / 内网软件源(脚本名 `set-os-repo.sh`)
3. **`03-set-ssh-keypair`** —— 把仓库内的 `id_rsa` / `id_rsa.pub` 分发到 `/root/.ssh`,配置 `authorized_keys` 实现节点间互信
4. **`04-install-chrony`** —— 安装 chrony,以 `timeServer` 标记的节点为服务端、其他节点为客户端
5. **`05-set-os-kernel`** —— 内核参数调优(sysctl 转发 / bridge / IPVS 等)

### 4.3 master 阶段

`master/master.yaml` 串行执行:

1. **`01-install-registry`** —— 仅 `{{ registryServer.hostName }}`(剧本标记的镜像仓库主机)安装 Docker Registry
2. **`02-install-containerd`** —— 所有节点安装 containerd
3. **`03-install-kubeadm_kubelet`** —— 所有节点安装 kubeadm / kubelet / kubectl,然后:
   - `apiserverLB` 主机执行 `init-master.sh` 初始化第一个 master
   - 其他 master 节点以 **`serial`** 模式逐个 `kubeadm join control-plane`(顺序敏感!)
   - worker 节点以 **`serial`** 模式逐个 `kubeadm join`
   - `apiserverLB` 主机执行 `untaint_control_plane.sh` 解除控制平面污点

### 4.4 tools 阶段(apiserverLB 节点)

`tools/tools.yaml` 在 `apiserverLB` 节点上依次安装:

- CNI(由 `01-install-CNI/install-cni.sh`)
- Helm CLI(`02-helm/install-helm.sh`)
- Kubernetes Dashboard(`03-kubernetes-dashboard/install-dashboard.sh`,**serial**)
- Traefik(`04-traefik/install-traefik.v3.0.2.sh`)
- NFS subdir external provisioner(`05-nfs-sc/install-nfs.sc.sh`,部署到 `{{ nfsServer }}`)
- debug 工具集(`06-debug/install-debug-tools.sh`)

---

## 5. 预检与连通性探测(apply 启动阶段)

参考 [`cmd/apply.go`](../cmd/apply.go):

### 5.1 预检(打印 + Y/N)

```text
配置文件已读取到以下组信息：
  1. group "masters": 3 host
  2. group "workers": 2 host
  3. group "allnode": 5 host

k8s节点信息确认：
  1. 所有 k8s 节点 /data 目录和 data 磁盘正确挂载了吗？
  2. 所有 k8s 节点 /etc/resolv.conf DNS 解析都正常设置了吗？
  3. 所有 k8s 节点时区设置正确了吗？
  4. 所有 k8s 节点 OS 软件源设置正确了吗？

是否继续? [Y/N]
```

- 回车 / `Y` / `y` / `yes` → 继续
- `N` / `n` / `no` → 立即退出
- 其他 → 视为非法,中止
- ⚠️ **空回车默认 yes** —— CI / 脚本场景请明确传 `Y` 或用 `--skip-probe`

### 5.2 连通性探测(SSH `echo pong`)

参考 [`internal/runner/ping/ping.go`](../internal/runner/ping/ping.go):

- 对每个主机开 SSH session,跑 `echo pong`
- **校验 stdout 必须等于 `pong`**(非零退出 / 输出不符都判失败)
- 任一主机不可达 → 整个 apply 立即终止,报告 `connectivity probe: N/M host(s) unreachable`
- 可用 `--skip-probe` 跳过(已用 `steel ping` 验证过的 CI 场景)
- 可用 `--probe-timeout` 调整单主机拨号超时(默认 3s)

---

## 6. Play 执行模式

### 6.1 默认并发(fan-out)

参考 [`internal/runner/apply.go`](../internal/runner/apply.go):

- **Play 之间**:顺序执行,前一个 Play 全部主机成功后才进入下一个
- **Play 内部**:`hosts:` 解析出的所有主机**默认并发** SSH 执行
- 任意一个主机失败 → fail-fast,整个 apply 终止

### 6.2 串行模式(`execution_mode: serial`)

针对顺序敏感的场景(etcd bootstrap / kubeadm join),play 级可切串行:

```yaml
- name: other masters join k8s cluster
  hosts: masters
  shell:
    cmd: /bin/bash join-k8s-on-the-master1.sh control-plane
    chdir: "{{ data_root }}/master/03-install-kubeadm_kubelet"
    bash: /bin/bash
  execution_mode: serial     # ← 强制一台一台来
```

只有字面量 `"serial"` 触发串行,其他值(包括拼错、空字符串)都走默认并发。

---

## 7. 扩容流程(addworkers)

### 7.1 流程

1. **保留原入口文件**,在顶层 `addworkers:` 字段填入新节点:

   ```yaml
   hosts:
     masters: {...}
     workers: {...}

   addworkers:
     k8s_worker1:
       hostname: node1
       ip: 192.168.170.22
       user: root
       password: <同其他节点>
     # 可以多个
   ```

2. **修改 runlist**,只跑扩容相关子 playbook:

   ```yaml
   runlist:
     - addworkers/add-node.yaml
   ```

3. **重新执行**:

   ```bash
   steel apply install-k8s-yamls/install-k8s.yaml
   ```

### 7.2 为什么不会污染原集群?

`addworkers` 在 inventory 加载期就被**隔离**:

- 不进入自动构建的 `allnode` 组
- 不进入 `k8s_all_ip` / `k8s_all_hostname` 聚合变量
- 因此 etcd `--initial-cluster` / kubelet peer list 等 bootstrap 阶段的写入**不会**带这些节点

`addworkers/add-node.yaml` 内部所有 Play 都用 `hosts: addworkers` 显式命中,**不会**通过隐式 `allnode` 误带到原集群节点上。

`addworkers` 的子 playbook 与 `common/` 是**独立**的(都是 `01-set-etc-hosts.yaml` 但路径不同):

- `common/01-etc-hosts/` —— 装机时跑,所有初始节点
- `addworkers/01-set-etc-hosts.yaml` —— 扩容时跑,只命中 `addworkers` 节点

---

## 8. 日志与排障

### 8.1 日志位置

每次 apply 在 `logs/<UTC-时间戳>/` 下生成一个目录,里面只有一个 `run.log` 文件:

```text
logs/
└── 20260625-090142/
    └── run.log
```

整个 run(所有 play / 所有 host)按执行顺序追加,行内带 `host=` / `play=` 标记:

```text
run started

host=master1 play="01-etc-hosts:set-etc-hosts" started
…
host=master1 play="01-etc-hosts:set-etc-hosts" exit=0

host=master2 play="01-etc-hosts:set-etc-hosts" started
…
```

### 8.2 常用排障命令

```bash
# 查看某台主机的全部输出
grep '^host=master1 ' logs/<UTC-时间戳>/run.log

# 查看某个 play 的全部输出
grep 'play="01-etc-hosts:set-etc-hosts"' logs/<UTC-时间戳>/run.log

# 查看失败节点
grep 'exit=[1-9]' logs/<UTC-时间戳>/run.log

# 查看 exit code 与 stderr
grep -E 'exit=[1-9]|stderr' logs/<UTC-时间戳>/run.log
```

### 8.3 远端环境变量排障

`test-env.yaml` 是一个**开发期工具**,把 `cmd` 改成 `env` 后跑一次,就能看到所有 prelude 注入的环境变量:

```yaml
- name: all node env
  hosts: allnode
  shell:
    cmd: env
    bash: /bin/bash
```

或者只验某台特殊主机(用模板动态 target):

```yaml
- name: nfsServer env
  hosts: {{ nfsServer }}
  shell:
    cmd: env

- name: echo dns_domain
  hosts: {{ apiserverLB.hostName }}
  shell:
    cmd: echo "{{ dns_domain }}"
```

---

## 9. 安全提示

### 9.1 ⚠️ 关于 `kubeadm-1.29.5-*-100year`

`master/03-install-kubeadm_kubelet/` 下的 `kubeadm-1.29.5-amd64-100year` 与 `…-arm64-100year` **不是上游官方 kubeadm**——这两个二进制把证书有效期从默认 1 年改成了 **100 年**。这是社区常用的 patch,使用者请**自行评估**:

- 想用上游默认:把 `install-kubelet.sh` 里调用的 kubeadm 换成官方版,证书默认 1 年,届时需 `kubeadm certs renew`
- 想保留 100 年证书:接受"证书几乎与集群寿命等长"的事实
- 自行 patch:按相同思路重新构建

### 9.2 ⚠️ 已删除 / 不提交的开源二进制

为了把仓库干净开源,以下二进制 / 离线包**不包含在本仓库**,运行前必须自行下载:

| 路径                                                    | 用途                            | 上游                                                                |
| ------------------------------------------------------- | ------------------------------- | ------------------------------------------------------------------- |
| `master/01-install-registry/registry.{amd64,arm64}`     | Docker Distribution 镜像仓库    | <https://github.com/distribution/distribution/releases>             |
| `master/01-install-registry/image-tools-linux-*`        | 镜像导入 / 打 tag 工具          | 见其官方仓库                                                        |
| `master/01-install-registry/images.*.tar.gz`            | 离线镜像包(推送到 registry.local:5000) | 主要含 kubernetes 和各类插件离线镜像                       |
| `master/02-install-containerd/cri-containerd-cni-*.tar.gz` | containerd + CNI + cri 离线包 | <https://github.com/containerd/containerd/releases>                 |
| `master/03-install-kubeadm_kubelet/rpms/{amd64,arm64}/` | Kubernetes 1.29.5 离线 RPM      | <https://packages.kubernetes.io/> 或 openEuler EPOL                  |
| `tools/02-helm/helm-v3.13.1-linux-*.tar.gz`             | Helm CLI                        | <https://github.com/helm/helm/releases/tag/v3.13.1>                  |
| `tools/04-traefik/traefik-helm-chart-*.tar.gz`          | Traefik Helm Chart              | <https://github.com/traefik/traefik-helm-chart>                      |
| `tools/05-nfs-sc/nfs-subdir-external-provisioner.*.tar.gz` | NFS subdir external provisioner | <https://github.com/kubernetes-sigs/nfs-subdir-external-provisioner> |

`dist/` 目录与 `logs/` 目录均被 `.gitignore` 排除,**不要**把编译产物或日志提交进 PR。

### 9.3 🔒 请勿提交敏感信息

- `install-k8s-yamls/common/03-set-ssh-keypair/id_rsa` / `id_rsa.pub` —— SSH 私钥对,**不要**提交,本地生成后自行分发
- 任何 playbook / `*.yaml` 里直接写入的 `password:` —— 建议改用 `vars:` 注入或 vault 外部化,否则 IP / 密码 / 私钥一旦 push 到 GitHub 视为泄露
- `logs/` 下的 `run.log` 可能含 IP / 主机名 / 命令输出 —— 已在 `.gitignore` 排除,提交前请 `git status` 复核

### 9.4 把缺包跑起来的最快路径

1. 按上表"上游下载"列,把二进制 / 压缩包下载到对应目录
2. 重新生成自己的 SSH 密钥对,放到 `common/03-set-ssh-keypair/`
3. 把示例 playbook 里的 `password:` 改成你自己的凭据(或者直接走 `ssh_key`)
4. `go build -o steel .` 自己编译 steel
5. `steel apply install-k8s-yamls/install-k8s.yaml`

---

## 10. 与传统 Ansible 装机流程的对比

| 维度             | Steel + install-k8s-yamls                   | 传统 Ansible                                       |
| ---------------- | ------------------------------------------- | -------------------------------------------------- |
| 运行时           | 单 Go 二进制                                 | Python + 大量依赖                                  |
| 远端准备         | 无 agent(SSH 即可)                          | 需要 Python / 依赖收集(`ansible facts`)           |
| 主机清单         | 入口 YAML 顶层 `hosts:` keyed-map            | 独立 INI / YAML                                    |
| 变量             | `vars:` 块 + 自动派生(`<key>_ip` 等)        | `group_vars` / `host_vars` 多级                    |
| 远端环境         | prelude 自动注入(`$os_arch` / `$k8s_*`)     | Ansible facts 自行收集                             |
| 预检             | 内置 4 条提示 + SSH 连通性探测              | 无内置,需写 pre_tasks                              |
| 串行 / 并发      | play 级 `execution_mode: serial` 可切       | `serial:` / `throttle:`                            |
| 模板             | 极简 `{{ xxx }}`                            | Jinja2 全功能                                       |
| 调试             | `logs/<UTC-时间戳>/run.log` 单文件 + `host=` / `play=` 标记 | 终端实时输出 + 异步回调                |
| 适用场景         | 文件分发 + 远程命令的轻量 K8s 装机           | 全功能配置管理                                      |

---

## 11. 进阶:自定义拓扑

### 11.1 给某台主机打多个角色

`plugin:` 是一个 map,可以同时声明多个角色,每个角色都会派生出对应的内置变量:

```yaml
k8s_master2:
  hostname: master2
  ip: 192.168.170.49
  plugin:
    registryServer: { alias: registry.local }
    nfsServer: true
    apiserverLB:    { alias: apiserver.cluster.local }
```

这台 master2 同时是 registry / nfs / apiserverLB 角色,在 master2 跑 shell 时能读到:

```text
registryServer_hostName=master2
registryServer_ip=192.168.170.49
registryServer_alias=registry.local
nfsServer=master2
nfsServer_ip=192.168.170.49
apiserverLB_hostName=master2
apiserverLB_ip=192.168.170.49
apiserverLB_alias=apiserver.cluster.local
```

### 11.2 自定义组(如 `etcd` / `lb`)

```yaml
hosts:
  masters: {...}
  workers: {...}

# 顶层 list 兄弟字段
etcd: [masters]      # 组成员是 hostname
lb: [master1]        # 只有 master1
```

Play 里直接 `hosts: etcd` / `hosts: lb` 命中。

### 11.3 拓扑感知调度(动态 hosts)

`hosts:` 字段支持模板,可以**按变量决定目标**:

```yaml
- name: 在镜像仓库主机上 install registry
  hosts: {{ registryServer.hostName }}      # → master2
  shell:
    cmd: /bin/bash install_registry.sh

- name: 在 nfs 主机上 install nfs-sc
  hosts: {{ nfsServer }}                   # → master2
  shell:
    cmd: /bin/bash install-nfs.sc.sh
```

只要 `vars.registryServer.hostName` / `vars.nfsServer` 在 `vars:` 块里写了 hostname,就能动态找到对应主机。


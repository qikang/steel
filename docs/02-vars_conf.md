# 变量与环境变量(vars / template / prelude)

> 本节讲 Steel 的两套变量系统:**模板渲染变量**(`{{ xxx }}`)与**远端 Shell 环境变量**(`$VAR`)。前者用于本地拼装 `cmd` / `chdir` / `hosts` / `src` / `dest`,后者用于远端脚本直接读。

完整源码位置:
- [`internal/vars/render.go`](../internal/vars/render.go) / [`internal/vars/expand.go`](../internal/vars/expand.go) —— 模板渲染与派生
- [`internal/runner/shell/prelude.go`](../internal/runner/shell/prelude.go) —— 远端 Shell 环境变量拼装
- [`internal/runner/derived_vars.go`](../internal/runner/derived_vars.go) —— 自动派生变量计算
- [`internal/runner/facts.go`](../internal/runner/facts.go) —— host facts

---

## 1. vars 块(全局参数)

入口 YAML 顶层声明一个 `vars:` 块,定义全局参数。所有 Play 在执行时都能读到这同一份。

```yaml
vars:
  pod_network_cidr: 10.244.0.0/16
  service_cidr: 10.96.0.0/12
  dns_domain: cluster.local
  data_root: /data/install-k8s-remoteDirectory
  k8s_network_stack: ipv4    # ipv4 / ipv6 / dual-stack
```

支持嵌套 map,字段间用 `.` 访问:

```yaml
vars:
  registryServer:
    alias: registry.local
    hostName: master2       # 这台主机是谁
  nfsServer: master2        # 顶层字符串直接写主机名,会自动派生 nfsServer_ip
```

---

## 2. 模板渲染(`{{ xxx }}`)

`{{ xxx }}` 在以下字段里都会渲染:
- `shell.cmd` / `shell.chdir` / `shell.bash`
- `copy.src` / `copy.dest`
- `hosts`(play 目标)
- `name`(play 名)
- runlist 子文件名 / 子文件内任意字段

### 2.1 查找顺序(bare name,无 `.`)

参考 [`internal/vars/render.go::resolve`](../internal/vars/render.go):

1. **vars 顶层** —— 用户在 `vars:` 块里声明的 key,优先于所有 builtin,允许同名覆盖
2. **host builtins** —— `hostname` / `hostip` / `username` / `password` / `sshkey` / `os_arch` / `master_node_number`
3. **inventory builtins** —— `{{ all_<group>_hostname }}`,组名**单数**形式(如 `all_master_hostname` 命中 `masters` 组),值是空格分隔的 hostname 列表;合成组(如 `allnode`)不暴露

未知占位符**立即报错**(`placeholder "xxx" not found`),不会留到远端运行。

### 2.2 带 `.` 的占位符

- `host.<field>` → 当前 host 事实字段(`name` / `ip` / `user` / `port` / `ssh_key` / `password` / `os_arch` / `master_node_number`)
- 其他首段(如 `{{ registryServer.hostName }}`)→ 走 `vars` map,逐层 walk

```yaml
shell:
  cmd: echo {{ registryServer.hostName }}    # → master2
  cmd: echo {{ host.ip }}                    # → 当前主机 IP
```

### 2.3 在 `hosts:` 字段里用模板(动态目标)

Play 的 `hosts:` 字段也走模板,可以引用任意变量求值:

```yaml
- name: echo dns_domain
  hosts: {{ apiserverLB.hostName }}          # → "master2" 或 "master3"
  shell:
    cmd: echo "{{ dns_domain }}"

- name: 在 nfsServer 上装 nfs-sc
  hosts: {{ nfsServer }}                     # → "master2"
  shell:
    cmd: /bin/bash install-nfs.sc.sh
    chdir: "{{ data_root }}/tools/05-nfs-sc"
```

`hosts:` 字段解析流程(参考 [`internal/runner/apply.go::renderHostsSpec`](../internal/runner/apply.go)):

1. 模板渲染后得到一个**字符串**(主机名 / 组名)
2. 把字符串交给 [`inventory.Hosts`](../internal/inventory/resolve.go) 解析
3. 单主机名 → 1 个 target;组名 → 该组所有成员(递归展开 + 去重)

⚠️ **注意**:`hosts:` 渲染时 host context 还没确定,因此**不能用 `{{ hostname }}` / `{{ host.ip }}` 这类 per-host 变量**——只能引用 vars 块或 inventory builtins(如 `{{ all_master_hostname }}`)。

---

## 3. 派生变量(自动注入,用户声明优先)

`vars:` 块在喂给 prelude 之前会被 [`internal/runner/apply.go`](../internal/runner/apply.go) 串行叠加以下几层派生,**用户显式声明一律优先**(不会覆盖已有同名 key):

| 增强                      | 触发条件                                   | 行为                                                                                                                                  | 实现                                                                                |
| ------------------------- | ------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------- |
| `plugin:` 字段派生        | 主机 hosts-block 下存在 `plugin:`          | 把布尔派生为 `<key>: <hostName>`;把 map 派生为 `<key>: { hostName, ip, ...原字段 }`                                                  | [`runner/derived_vars.go::derivePluginVars`](../internal/runner/derived_vars.go)    |
| 嵌套 map 自动注入 `ip`    | 任意层级出现 `hostName: <某主机名>`        | 在同一 map 内新增兄弟字段 `ip: <该主机 IP>`,只在 `ip` 未显式声明时                                                                    | [`vars/expand.go::ExpandHostNameIPs`](../internal/vars/expand.go)                   |
| 顶层字符串派生 `<key>_ip` | 顶层 `vars.<key>: <某主机名字符串>`        | 新增顶层 `<key>_ip: <该主机 IP>`;非字符串 / 空串 / 主机名解析不到不派生                                                              | [`vars/expand.go::ExpandTopLevelHostnameIPs`](../internal/vars/expand.go)           |
| `noapiserverips`          | `vars.apiserverLB.hostName` 解析到 master  | 注入 `noapiserverips="<其他 master 的 IP,空格分隔>"`                                                                                  | [`runner/derived_vars.go::computeNoApiserverIps`](../internal/runner/derived_vars.go) |
| `k8s_all_ip`              | 用户未在 vars 显式声明                     | `inv.PrimaryHosts()`(排除 `addworkers`)按 IP 空格拼接                                                                                  | [`runner/derived_vars.go::computeK8sAllIP`](../internal/runner/derived_vars.go)       |
| `k8s_all_hostname`        | 用户未在 vars 显式声明                     | 同上,按 `Host.Name` 拼接                                                                                                              | [`runner/derived_vars.go::computeK8sAllHostname`](../internal/runner/derived_vars.go) |

### 3.1 实际例子

`install-k8s-yamls/install-k8s.yaml` 中:

```yaml
hosts:
  masters:
    k8s_master2:
      hostname: master2
      plugin:
        registryServer: { alias: registry.local }
        nfsServer: true
```

派生结果(用户在 shell 中可读到的环境变量):

```text
registryServer_hostName=master2         # 嵌套 map 派生
registryServer_ip=192.168.170.49        # 嵌套 map 派生
registryServer_alias=registry.local     # 嵌套 map 字段直通
nfsServer=master2                       # 布尔派生
nfsServer_ip=192.168.170.49             # 顶层字符串派生
```

---

## 4. 远端 Shell 环境变量(prelude)

每一个 `shell` 模块执行前,Steel 拼接一段 `export k=v; export k2=v2; ...;` 前置脚本,然后通过 `bash -l -c "<prelude>; <user-cmd>"` 在远端登录 shell 里跑出去。所有 `export` 的变量都会被该 shell 以及它派生的子进程继承,远端脚本里直接 `$VAR` 或 `${VAR}` 引用即可。

### 4.1 注入顺序(固定)

| #   | 变量                            | 来源                                                                                                            | 业务目的                                                               |
| --- | ------------------------------- | --------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------- |
| 1   | `os_arch`                       | 首连该主机时 `uname -m`,归一化 `x86_64→amd64` / `aarch64→arm64`,按 `host.Name` 缓存                          | 远端脚本按 `$os_arch` 选 amd64 / arm64 二进制路径                      |
| 2   | `master_node_number`            | `inv.MasterCount()`,inventory.New 强约束为 `1` / `3` / `5`                                                       | etcd `--initial-cluster`、kubeadm bootstrap 拓扑判断                   |
| 3   | `k8s_master1_ip`                | hosts-map key 归一化为 `k8s_master1` 的那台主机 IP                                                              | bootstrap 阶段总是要第一个 master 的地址                               |
| 4   | `k8s_hosts_alias`               | 全清单 `ip hostname` 对,字面量 `\n ` 分隔(可直接 `echo -e "$k8s_hosts_alias" >> /etc/hosts`)                    | 一次性把整张主机表喂进远端 `/etc/hosts`,省掉循环                       |
| 5   | `<key>=<value>`(扁平 vars)      | 入口 `vars:` 块标量(经派生增强后)                                                                               | 用户直接声明的标量参数                                                 |
| 6   | `<a>_<b>=<v>`(嵌套 map flatten) | 嵌套字段,`.` 用 `_` 拼接(如 `registryServer.hostName` → `registryServer_hostName`)                            | 把 map 拍平喂给 shell,远端脚本直接 `echo $registryServer_hostName`     |

> 步骤 1–4 是**逐主机、按本次执行唯一**注入的(`os_arch` 走缓存,后续 play 不再 SSH 拨号);步骤 5–6 来自入口 YAML 顶层 `vars:`,所有主机共用同一份。

### 4.2 实际日志样例

`logs/20260625-090142/run.log` 里跑 `printenv` 看到的现场:

```text
os_arch=amd64
master_node_number=1
k8s_master1_ip=192.168.170.48
k8s_hosts_alias=192.168.170.47 ip-192-168-170-47\n 192.168.170.48 ip-192-168-170-48\n 192.168.170.49 ip-192-168-170-49
k8s_all_ip=192.168.170.48 192.168.170.49
k8s_all_hostname=ip-192-168-170-48 ip-192-168-170-49
noapiserverips=192.168.170.49                    # 仅当 apiserverLB.hostName 命中某个 master 时注入
registryServer_hostName=ip-192-168-170-48        # vars.registryServer.hostName 拍平
registryServer_alias=registry.local              # vars.registryServer.alias 拍平
registryServer_ip=192.168.170.48                 # 嵌套 map 派生 ip
traefikServer_ip=192.168.170.48                  # 顶层字符串派生 _ip
nfsServer_ip=192.168.170.48                      # 同上
apiserverLB_ip=192.168.170.48                    # 同上
pod_network_cidr=10.244.0.0/16                   # vars 顶层标量,直接 export
service_cidr=10.96.0.0/12                        # 同上
dns_domain=cluster.local                         # 同上
```

### 4.3 值编码与注入策略

- 标量值走 [`prelude.go::writeEnvFromAny`](../internal/runner/shell/prelude.go),统一用 [`quote.go::shellQuote`](../internal/runner/shell/quote.go) **单引号包裹**——空格、单引号、反斜杠都会被转义,保证 prelude 在远端 bash 解析时是合法赋值
- 嵌套 map 递归拍平,父子段用 `_` 连接;map 的 key 在导出前按字典序排序,**逐字节稳定**便于日志 diff
- 数组 / 未知类型 fallback 到 `fmt.Sprintf("%v", v)`,不静默丢字段
- prelude 与用户命令之间用 `; ` 隔开(不是空格)——空格会把用户第一个 token 拼到上一个 `export` 的参数里触发 `export: not a valid identifier`

---

## 5. os_arch 与 master_node_number(关键内置变量)

这两个变量是**所有 shell 模块自动可用**的,**不需要**在 `vars:` 块里声明。

### 5.1 os_arch

- **来源**:首次访问主机时 SSH 执行 `uname -m`,归一化为 `amd64` / `arm64`
- **支持范围**:仅 `x86_64` / `aarch64`,其他架构(`ppc64le` / `riscv64` 等)直接 fatal
- **缓存**:`archCache` 按 `host.Name` 命中,后续 play 不再拨号;探测失败**不缓存**,下次重试
- **使用**:

```bash
curl -fsSL https://example.com/bin.$os_arch.tar.gz | tar xz
```

### 5.2 master_node_number

- **来源**:`inv.MasterCount()`,由 `inventory.New` 强约束为 `1` / `3` / `5`
- **使用场景**:etcd `--initial-cluster` / kubeadm bootstrap 拓扑判断

```bash
case "$master_node_number" in
  1) ETCD_QUORUM=1 ;;
  3) ETCD_QUORUM=2 ;;
  5) ETCD_QUORUM=3 ;;
esac
```

---

## 6. 集群聚合变量(自动派生)

未在 `vars:` 显式声明时,Steel 会注入以下聚合变量(`addworkers` 始终排除):

| 变量名              | 值                                                       | 用途                                       |
| ------------------- | -------------------------------------------------------- | ------------------------------------------ |
| `k8s_master1_ip`    | hosts-map key `k8s_master1` 那台的 IP                    | bootstrap 第一个 master 的地址             |
| `k8s_hosts_alias`    | 全清单 `ip hostname\n ip hostname` 字符串                | 直接 `echo -e "$k8s_hosts_alias" >> /etc/hosts` |
| `k8s_all_ip`         | 空格分隔,所有初始节点 IP(排除 `addworkers`)             | etcd peer list / kubelet peer list         |
| `k8s_all_hostname`  | 空格分隔,所有初始节点 hostname                           | 与 `k8s_all_ip` zip 还原 `ip hostname` 对   |
| `noapiserverips`    | 当 `apiserverLB.hostName` 命中 master 时,空格分隔其他 master IP | kube-apiserver 自身从 list 中剔除   |

⚠️ **用户显式声明一律优先**:在 `vars:` 块里手动写 `k8s_all_ip: "..."` 会让派生逻辑跳过,**不会被覆盖**。

---

## 7. plugin 派生变量(host 角色 → env)

host 的 `plugin:` 字段会按角色产生对应的环境变量,**只对那台主机有效**(per-host):

| hosts-block 写法                          | 该主机能读到的环境变量                                                              |
| ----------------------------------------- | ----------------------------------------------------------------------------------- |
| `traefikServer: true`                     | `traefikServer=master1`,`traefikServer_ip=192.168.170.48`                            |
| `nfsServer: true`                         | `nfsServer=master2`,`nfsServer_ip=192.168.170.49`                                    |
| `registryServer: { alias: registry.local }` | `registryServer_hostName=master2`,`registryServer_ip=...`,`registryServer_alias=registry.local` |
| `apiserverLB: { alias: apiserver.cluster.local }` | `apiserverLB_hostName=master2`,`apiserverLB_ip=...`,`apiserverLB_alias=apiserver.cluster.local` |
| `timeServer: true`                        | `timeServer=master3`,`timeServer_ip=...`                                             |

这些派生的好处是 **bootstrap 脚本不需要再 "查表"**——直接 `$nfsServer_ip` / `$registryServer_hostName` 就能用。

---

## 8. 常见错误清单

| 写法                                                | 报错信息                                          | 修正                                                                       |
| --------------------------------------------------- | ------------------------------------------------- | -------------------------------------------------------------------------- |
| `{{ xxx }}` 但 `xxx` 未声明                          | `placeholder "xxx" not found`                     | 在 `vars:` 块声明,或改成已知的 builtin                                     |
| `{{ hostname }}` 出现在 `hosts:` 字段              | 渲染失败(host context 还未确定)                  | 改用 inventory builtin(`{{ all_master_hostname }}`)或 vars 引用            |
| `vars.nfsServer: master99`(主机名拼错)             | 派生阶段静默跳过(不报错,但 `$nfsServer_ip` 空)   | 校对 `hostname:` 字段,确保能命中                                           |
| `vars.masters: [m1, m2]` 写成 list                  | 不影响派生,但 `{{ masters }}` 拿不到              | 直接用 `masters` / `workers` 这种**组名**做 hosts 目标                    |
| `chdir: "{{ data_root }}"` 漏引号                  | YAML 把 `{{ data_root }}` 解析成 flow mapping     | 整个值用双引号包起(YAML 自动转义)                                         |
| 期望 `os_arch=amd64` 但实际是空                     | 探测阶段失败 / 主机不可达                        | 先 `steel ping <playbook>` 确认连通,再看 `logs/<ts>/run.log`             |

---

## 9. 与 Ansible vars / templates 的差异

| 维度         | Steel                                       | Ansible                                                 |
| ------------ | ------------------------------------------- | ------------------------------------------------------- |
| vars 存放    | 入口 YAML 顶层 `vars:` 块                   | `group_vars/` / `host_vars/` / play `vars:` 多级        |
| 模板引擎     | 内置极简 `{{ xxx }}` 实现                   | Jinja2 全功能                                            |
| 派生变量     | 在 Go 进程内静态派生(派生表见 §3)           | `hostvars` / `group_vars` 运行时合并                    |
| 内置变量     | `os_arch` / `master_node_number` / `k8s_*`  | `ansible_hostname` / `ansible_architecture` / `groups`  |
| 远端注入     | `export k=v; export k2=v2; <cmd>` prelude    | 通过 SSH 环境 / `--extra-vars` 文件                     |
| 特殊变量     | `hostName → _ip` 自动派生                    | 无内置,需手写 inventory plugin                          |


# 主机清单(Inventory)书写规范

> Steel **不接受独立的 `hosts.yaml`**——所有主机信息必须写在入口 Playbook 顶层的 `hosts:` 块里。本节讲清单文件怎么写、内部规则、约束、注意事项。

完整源码位置: [`internal/inventory/`](../internal/inventory/)、[`internal/playbook/playbook.go`](../internal/playbook/playbook.go) 中的 `decodeTopLevelHosts`。

---

## 1. hosts 块结构

入口 YAML 顶层声明一个 `hosts:` 块,值是 mapping:

```yaml
hosts:
  masters:        # 组名
    k8s_master1:  # hosts-map key(决定 $k8s_master1_ip 这类内置变量名)
      hostname: master1            # SSH 实际连谁
      ip: 192.168.170.48          # IP / 可解析域名
      user: root                  # SSH 用户
      password: your_password     # 密钥或密码二选一
      port: 22                    # 可选,默认 22
      plugin:                     # 可选,见 §5
        traefikServer: true
    k8s_master2:
      hostname: master2
      ip: 192.168.170.49
      user: root
      ssh_key: ~/.ssh/id_rsa
  workers: {}   # 空组合法,占位用

allnode: [masters, workers]   # 可选,见 §4
addworkers: {}                # 可选,见 §6
```

- **keyed-map 形态是唯一支持的写法**——list-of-objects 形态(`masters: [ {...}, {...} ]`)会被解析器拒绝。
- 组名可以自由命名,常用规范:`masters` / `workers` / `addworkers` / `etcd` / `lb` 等。
- 组内每条记录的 **map key**(例如 `k8s_master1`)与 `hostname:` 字段是**两个独立字段**——前者决定 `k8s_<key>_ip` 这类内置变量名,后者决定 SSH 实际连谁。

---

## 2. 主机字段

| 字段                 | 必填 | 别名            | 说明                                                              |
| -------------------- | ---- | --------------- | ----------------------------------------------------------------- |
| `name` / `hostname`  | ✅    | 二选一          | 主机名 / SSH 连谁 / 模板 `{{ hostname }}`                          |
| `ip` / `hostip`      | ✅    | 二选一          | IP 或可解析域名                                                   |
| `user` / `username`  | ✅    | 二选一          | SSH 登录用户                                                      |
| `ssh_key` / `sshkey` | ✅*   | 二选一          | 私钥路径,`~` 自动展开                                            |
| `password`           | ✅*   | —               | 密码(未配密钥时使用)                                              |
| `port`               | ❌    | —               | SSH 端口,默认 22                                                 |
| `plugin`             | ❌    | —               | 角色 / 服务标记,见 §5                                             |

> `ssh_key` 和 `password` **至少填一个**。两者都填时优先 `ssh_key`。都不填且 `SSH_AUTH_SOCK` 存在时,回落本地 ssh-agent。**注意**:加密私钥(passphrase)当前不支持,会直接报错,请走 ssh-agent。

加载期校验(参考 [`internal/inventory/validate.go`](../internal/inventory/validate.go)):

- `hostname:` 缺失 → 报错
- `ip:` 缺失 → 报错
- `user:` 缺失 → 报错
- `ssh_key:` 与 `password:` 都缺失 → 报错(ssh-agent 路径只在前两者都缺时尝试)
- 同一 `hostname` 在不同组重复声明 → 报错(去重)

---

## 3. 组(groups)

组是 `hosts:` 块的 key,组成员既可以是**主机名**(`hostname:` 的值),也可以是**其他组名**——后者就是"组引用组"。

### 3.1 hosts 块下声明的隐式组

`hosts:` 下每个 key 自动成为一个组,成员是该组下所有主机的 `hostname` 列表:

```yaml
hosts:
  masters:
    k8s_master1: {hostname: master1, ip: 1.1.1.1, ...}
    k8s_master2: {hostname: master2, ip: 1.1.1.2, ...}
  workers:
    k8s_worker1: {hostname: node1, ip: 1.1.1.10, ...}
```

上面这个写法隐式声明了两个组:`masters` 和 `workers`,Play 可以直接 `hosts: masters` / `hosts: workers`。

### 3.2 顶层 `groups` 兄弟字段(显式组)

需要在 hosts 块外组合组时,在顶层加一个 list-of-strings 的兄弟键:

```yaml
hosts:
  masters: {...}
  workers: {...}

# 顶层兄弟字段
allnode: [masters, workers]   # 自定义聚合组
etcd: [masters]                # 只包含 masters 子集
```

注意:这些顶层 list-of-strings 是**纯组成员列表**,而 `hosts:` 下的同名组是 keyed-map 形态,**两者结构不同**:

- `hosts: {masters: {...}}` → 声明一组**主机**
- `masters: [k8s_master1, k8s_master2]`(顶层)→ 声明一个**组成员列表**,名字仍是 `masters` 但含义是"该组有哪些 hostname"

### 3.3 启动期校验

- 组成员名必须存在于 `hosts:` 内的 `hostname` 集合,或已经是另一个已声明的组
- 组循环(A → B → A)在加载期就拒(见 [`validate.go`](../internal/inventory/validate.go) 中的 `expandGroup`)
- 不允许同名组既在 `hosts:` 下,又在顶层 list 出现(冲突检测)

---

## 4. allnode 自动构建

`allnode` 是**内置组**,代表"所有参与初始 bootstrap 的节点"。

- **用户未显式声明 `allnode` 时**,Steel 自动构建一个:把 `hosts:` 下的所有组(按字典序)展开并拼接,**排除 `addworkers` 组**
- **用户显式声明 `allnode` 时**(例如 `allnode: [masters, workers, addworkers]`),用户声明优先生效——此时可包含 `addworkers`

```yaml
# 等价于自动构建
allnode: [masters, workers]

# 想包含 addworkers 节点必须显式声明
allnode: [masters, workers, addworkers]
```

⚠️ **生产建议**:保持自动构建语义,不显式声明 `allnode`,让 `addworkers` 节点天然排除在 bootstrap 阶段之外(etcd `--initial-cluster`、kubelet peer list 等不会写到它们上面)。

---

## 5. plugin 字段(角色标记)

`plugin:` 是 host 内的自由形态 map,常见两种用法:

```yaml
plugin:
  # 布尔形态:该主机是 X 服务
  traefikServer: true
  nfsServer: true

  # map 形态:带别名 / 配置
  registryServer:
    alias: registry.local
  apiserverLB:
    alias: apiserver.cluster.local
```

Steel 会把 `plugin:` 翻译成远端 shell 可用的内置变量:

| 写法                                | 派生的内置变量(注入到 prelude)                                                                 |
| ----------------------------------- | ---------------------------------------------------------------------------------------------- |
| `traefikServer: true`               | `traefikServer=<hostName>` + `traefikServer_ip=<IP>`                                            |
| `registryServer: { alias: x }`      | `registryServer_hostName=<hostName>` + `registryServer_ip=<IP>` + `registryServer_alias=x`     |
| `nfsServer: true`                   | `nfsServer=<hostName>` + `nfsServer_ip=<IP>`                                                   |
| `apiserverLB: { alias: x }`         | `apiserverLB_hostName=<hostName>` + `apiserverLB_ip=<IP>` + `apiserverLB_alias=x`             |

> `plugin:` 是 host 级别(每台主机的"我承担什么角色"),与 `vars:` 块(全局配置)是两层不同的语义。

详见 [02 - vars_conf.md](./02%20-%20vars_conf.md) 的"派生变量"章节。

---

## 6. addworkers 字段(扩容专用)

`addworkers` 是顶层 `hosts:` 的**兄弟字段**,用与 `masters` / `workers` 完全一致的 keyed-map 形态:

```yaml
hosts:
  masters: {...}
  workers: {...}

# 装机阶段留空
addworkers: {}

# 扩容阶段填入新节点
# addworkers:
#   k8s_worker1:
#     hostname: node1
#     ip: 192.168.170.22
#     user: root
#     password: your_password
```

### 6.1 关键语义

- **不参与自动构建的 `allnode`**——见 §4
- **不出现在 `k8s_all_ip` / `k8s_all_hostname` 聚合变量里**——这两个变量描述的是"初始 bootstrap 的节点集合",扩容节点不应出现在 etcd `--initial-cluster`、kubelet peer list、bootstrap 阶段的 `/etc/hosts` 写入目标中
- **没有 `masters` 那种 1/3/5 容量约束**——空 `addworkers: {}` 是合法且推荐的默认
- **Play 必须显式 `hosts: addworkers`** 才能命中这些节点,不会因为 "allnode" 这种隐式组而被误带跑

### 6.2 扩容用法

`install-k8s-yamls/addworkers/add-node.yaml` 是一份只命中 `addworkers` 的 playbook:

```yaml
# install-k8s-yamls/addworkers/add-node.yaml
runlist:
  - 01-set-etc-hosts.yaml
  - 02-set-os-repo.yaml
  - 03-install-keypair.yaml
  - 04-install-chrony.yaml
  - 05-set-kernel.yaml
  - 06-install-containerd.yaml
  - 07-install-kubelet.yaml
```

具体路径下每条子 playbook 内部都用 `hosts: addworkers`。重新跑入口文件 `install-k8s.yaml` 即可完成 join 流程。

---

## 7. masters 组规模约束(强校验)

`masters` 组**必须**恰好 **1、3 或 5 台**主机,其他规模在加载期直接报错(参考 [`validate.go::isValidMasterCount`](../internal/inventory/validate.go)):

- 0 / 2 / 4 / 6+ 全部被拒
- 没有 `masters` 组也直接拒
- 错误信息:`masters group has N hosts; steel supports only 1, 3, or 5 master nodes`

原因:etcd quorum 与 bootstrap 脚本只覆盖这三种规模(单 master / 3 HA / 5 HA),7 节点 HA 等不被支持的规模无法 bootstrap。

---

## 8. 完整示例(最小可装机清单)

`install-k8s-yamls/install-k8s.yaml` 是已验证的最小可装机清单,结构如下:

```yaml
hosts:
  masters:
    k8s_master1:
      hostname: master1
      ip: 192.168.170.48
      user: root
      password: <your-password>
      plugin:
        traefikServer: true
        registryServer: { alias: registry.local }
        nfsServer: true
        apiserverLB: { alias: apiserver.cluster.local }
        timeServer: true
    # k8s_master2 / k8s_master3 类似(3 HA 时)
  workers: {}
  addworkers: {}

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

具体可参考仓库里以下模板:

- [`install-k8s-yamls/install-k8s.yaml`](../install-k8s-yamls/install-k8s.yaml) —— 完整 3 master + n worker 模板
- [`install-k8s-yamls/1master-k8s.yaml`](../install-k8s-yamls/1master-k8s.yaml) —— 单 master 最小化
- [`install-k8s-yamls/3master_1node-k8s.yaml`](../install-k8s-yamls/3master_1node-k8s.yaml) —— 3 HA + 1 worker
- [`install-k8s-yamls/1master_2node-k8s.yaml`](../install-k8s-yamls/1master_2node-k8s.yaml) —— 单 master + 2 worker

---

## 9. 常见错误清单

| 写法                                                | 错误信息(典型)                                          | 修正                                                                  |
| --------------------------------------------------- | -------------------------------------------------------- | --------------------------------------------------------------------- |
| `masters: [{...}, {...}]`(list 形态)              | `hosts[masters] must be a mapping`                       | 改为 keyed-map:`masters: {k8s_master1: {...}}`                        |
| `hostname` / `ip` / `user` 缺失                      | `host "X" is missing 'ip'` 等                            | 三个必填字段都填                                                      |
| `ssh_key` 与 `password` 都缺失且无 ssh-agent         | `host "X" needs either 'ssh_key'/'sshkey' or 'password'` | 至少填一个;想用 ssh-agent 就放空两个并设置 `SSH_AUTH_SOCK`            |
| 同一 `hostname` 在多个组重复声明                    | `host "X" is declared more than once`                    | 用 hosts-map key 区分(同一个物理机不要出现两次)                        |
| `masters` 数量为 2 / 4 / 7                           | `masters group has N hosts; steel supports only 1, 3, or 5` | 改为 1 / 3 / 5;非支持规模直接拒                         |
| 组成员名写错                                         | `group "X" references unknown member "Y"`                | `Y` 必须是某条主机的 `hostname`,或另一个已声明组                       |
| 组循环 A → B → A                                     | `group cycle involving "X"`                              | 拆掉其中一个引用                                                      |
| 把 `addworkers` 主机写进 `hosts.masters` 下          | 不会报错,但语义错乱                                     | 严格区分:初始节点走 `masters` / `workers`,扩容节点走 `addworkers`      |

---

## 10. 与 Ansible Inventory 的差异

| 维度        | Steel                                  | Ansible                                    |
| ----------- | -------------------------------------- | ------------------------------------------ |
| 文件形态    | **内嵌在入口 Playbook 顶层 `hosts:`**  | 独立 `hosts.ini` / `hosts.yaml`,可用 `-i` 指定 |
| 组成员      | keyed-map(强制)                        | INI 段 / YAML list(灵活)                  |
| 子组        | 顶层 list-of-strings 兄弟字段          | `[group:children]` 段                      |
| 变量        | 单独写在 `vars:` 块                    | `[group:vars]` 段 / `group_vars/` 目录     |
| `addworkers` | 内置特殊语义(隔离 bootstrap)         | 无内置对应,需手工编排                      |


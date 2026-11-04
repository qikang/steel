# steel

`steel` 是一个用于安装 k8s 的命令行工具，其设计原理参考 Ansible 的 `copy` 与 `shell` 模块。

1. 通过 `copy` 将本地文件分发到远程主机
2. 通过 `shell` 在远程主机上执行 Linux 命令或脚本
3. 支持按规则预置自定义环境变量
4. `steel` 仅依赖 Go 标准库，不引入任何第三方类库
5. 理论上只要目标操作系统支持 k8s，即可完成对应架构的 k8s 部署

---

## 编译 steel

```bash
git clone https://github.com/qikang/steel.git
cd steel
go build -o steel .
```

---

## 安装示例

- **[install-k8s-yamls](install-k8s-yamls/) 目录安装方法已验证。**

### 测试连通性

```bash
steel ping install-k8s-yamls/install-k8s.yaml
```

### 执行安装

```bash
steel apply install-k8s-yamls/install-k8s.yaml
```

---

## [说明文档](docs/)

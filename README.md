# steel
steel是一个安装k8s的工具,原理参考 Ansible 的 `copy` 和 `shell` 模块，开发的工具。`copy`本地shell脚本到远程主机,使用`shell`执行脚本，也可根据规则预置自定义环境变量。<br>
steel 只是copy文件到远程服务器，然后在远程服务器执行shell脚本，所以只要操作系统环境支持k8s，理论上可以安装任意架构，任意系统的k8s。

---
## 编译steel

```bash
git clone https://github.com/qikang/steel.git
cd steel
go build -o steel .
```

---
## 安装示例
- **[install-k8s-yamls](install-k8s-yamls/) 目录安装方法已验证。** <br>

测试连通性
```bash
steel ping install-k8s-yamls/install-k8s.yaml
```

执行安装
```bash
steel apply install-k8s-yamls/install-k8s.yaml
```

<br>

---
[说明文档](docs/)


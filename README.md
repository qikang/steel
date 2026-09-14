# steel
steel是一个安装k8s的工具,原理参考 Ansible 的 `copy` 和 `shell` 模块，开发的工具。`copy`本地shell脚本到远程主机,使用`shell`执行脚本，也可根据规则预置自定义环境变量。

---

## 特性

- 🚀 **单二进制部署** —— Go 单产物即可运行,远端无需 agent
- 📦 **copy 模块** —— 类似 Ansible `copy`,支持文件 / 目录树,自动建中间目录、可指定权限位
- 💻 **shell 模块** —— 登录 Shell(`bash -l`)执行命令,支持 `chdir:`,默认请求 PTY
- 🌍 **环境变量自动注入** —— 每个远程命令前自动拼接 `export k=v; ...;`,导出架构 / 集群事实 + 嵌套 vars 平铺后的键值对
- 🏷️ **架构自动检测** —— 首次访问主机 SSH 执行 `uname -m`,归一化为 `amd64` / `arm64`,通过 `{{ os_arch }}` 暴露;仅支持 `x86_64` / `aarch64`
- 👥 **主机分组** —— 顶层 `hosts:` keyed-map,组可引用组(启动期检测 cycle);未声明 `allnode` 时自动构建,**默认排除 `addworkers`**
- 🔑 **认证** —— SSH 私钥 > 密码 > ssh-agent 回落;**不校验主机指纹**,生产请走外层网络隔离
- 📋 **Runlist 编排** —— 多 Playbook 顺序拼接,play 名格式 `<basename>:<原play名>`
- 🛡️ **预检** —— apply 启动前打印组信息 + 4 条操作员自检提示,单次 Y/N 确认(**空回车默认 yes**)
- 🔌 **连通性探测** —— SSH 跑 `echo pong`,任一不可达即拒绝继续;支持 `--skip-probe` / `--probe-timeout`
- 🪵 **单文件全量日志** —— 所有输出落到 `logs/<UTC-时间戳>/run.log`,按执行顺序追加,行内带 `host=` / `play=` 标记


---
## 编译steel

```bash
git clone https://github.com/qikang/steel.git
cd steel
go build -o steel .
```

---
## 安装示例
-  **install-k8s-yamls目录安装方法已验证。**<br>
- **install-k8s-yamls目录开源的二进制文件已删除，现在放置空文件占位！** <br>

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


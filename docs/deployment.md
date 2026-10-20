# 独立部署

## 构建

需要 Go 1.26+、Node.js 24+，SQLite 驱动无需 CGO。

```sh
bash scripts/build.sh
go test -race ./...
go vet ./...
```

## NAS

先确认架构、端口和目录权限。不要复用以前 MAA 管理器的数据卷。

```sh
./bin/maa-setup-linux-amd64 --dir private --host YOUR_NAS_DNS_OR_IP --port 18443 --container
mkdir -p data
chmod 700 private data
docker build -t maa-control-center:0.1.0 .
```

初始化拒绝覆盖已存在的密钥与配置。私有目录的 `admin.txt` 保存初始登录信息；`server.private.json` 是本机配置，含配对令牌，不得分享或提交 Git。

部分 NAS 没有 Docker 构建插件。此时可将已经核验校验和的 Linux 单程序放入新的 `rootfs/maa-server`，设为 755，再将该独立目录打包并通过 `docker import --change 'ENTRYPOINT ["/maa-server","--config-file","/config/server.private.json"]' - maa-control-center:0.1.0` 导入。实际部署已验证此方式；镜像不包含任何私有配置、证书或数据库。

新建独立容器，将 `private` 只读挂载到 `/config`，将 `data` 挂载到 `/data`；运行用户必须拥有数据目录。仅向实际局域网地址绑定新端口，使用只读根文件系统、权限限制与日志轮转：

```sh
docker run -d --name maa-control-center --restart unless-stopped \
  --user "$(id -u):$(id -g)" --read-only --cap-drop ALL \
  --security-opt no-new-privileges --pids-limit 64 --memory 128m \
  --log-opt max-size=5m --log-opt max-file=3 \
  -p YOUR_LAN_IP:18443:18443 \
  -v "$PWD/private:/config:ro" -v "$PWD/data:/data" \
  maa-control-center:0.1.0
```

也可直接运行单程序：移除 setup 的 `--container`，然后用 `maa-server --config-file private/server.private.json`。默认不允许非回环 HTTP，推荐 HTTPS。私有 CA 需要在客户端明确受信任，Agent 则通过 `ca` 文件验证；不关闭证书校验。CA 私钥不挂载给 Agent。

## Windows

1. 在新目录存放 Agent 二进制、`ca.pem` 和由示例填写的 `agent.private.json`。确认目录 ACL 仅管理员和运行用户可读。
2. `maa_dirs` 填入实际已有安装；`allow_read_dirs` 只包含需要读取的基建目录。不要依据旧目录名推断程序版本。
3. 配置 `server` 为实际 HTTPS 地址，`token` 与 NAS 配对一致；不要将 token 作为命令参数。
4. 首先执行 `maa-agent-windows-amd64.exe --config-file agent.private.json --once`；服务返回 accepted 才算心跳已通。
5. 使用 `scripts/install-agent.ps1 -InstallDir <新目录>` 建立当前用户登录任务。安装脚本拒绝覆盖同名任务。

任务只读，不启动、停止或重装 MAA / MuMu，也不更改现有定时器。不需要 SYSTEM 服务，不假设 Session 0 可以操作桌面。

若 Windows 限制脚本文件执行，可由受授权终端按安装脚本的明确步骤注册任务。实际部署使用隐藏的 PowerShell 命令启动已核验的 Agent，并等待其退出，未更改系统执行策略。任务注册成功不等于已经通过整机重启验证。

## 首次绑定

管理员登录 → MAA 账号 → 管理绑定 → 从实际发现结果选择配置档 → 对照启动账号标识保存。绑定只写本项目数据库。随后核对各自的任务、预算与排班。

## 卸载与恢复

Windows 使用 `scripts/uninstall-agent.ps1`，仅停止并移除本项目的任务，保留数据。NAS 只停止本项目容器，保留私有目录和数据库；不得用批量清理命令删除现有卷。

升级前使用 SQLite backup API 验证备份，在独立版本目录部署新二进制。回滚时停止新容器，恢复上一版本与经过完整性检查的数据库副本，不修改 Windows MAA 配置。

具备 sqlite3 CLI 的 NAS 可以使用 `.backup` 写入一个全新的备份路径，并以 `PRAGMA integrity_check;` 检查结果。无 Python 时也可仅停止本项目容器，确认 SQLite 已关闭且 WAL 已检查点落盘，再复制数据库到新文件并恢复服务；不停止其他容器。实际初始备份已通过完整性检查及隔离恢复验证。

# 自托管分支更新说明

使用 **install-custom.sh** 或 **docker-compose.custom.yml** 部署的自托管分支，更新代码后只需**一条命令**完成拉取、重建、重启，无需在服务器上安装 Go 或 Node（编译在 Docker 内完成）。

## 更新方式（任选其一）

### 方式一：一键脚本（推荐）

在**任意目录**执行，脚本会进入默认安装目录 `$HOME/nofx` 并完成 pull、构建、重启：

```bash
curl -fsSL https://raw.githubusercontent.com/tingdongai3-max/nofx/my-custom-version/install-custom.sh | bash
```

若安装目录不是 `$HOME/nofx`，可指定：

```bash
curl -fsSL https://raw.githubusercontent.com/tingdongai3-max/nofx/my-custom-version/install-custom.sh | bash -s -- /opt/nofx
```

### 方式二：已手动 git pull 后，只重建并重启

若已在服务器上执行过 `git pull`，在**安装目录**下执行一条命令即可。

**Docker Compose V2（`docker compose` 带空格）：**
```bash
cd ~/nofx && docker compose -f docker-compose.custom.yml build --no-cache && docker compose -f docker-compose.custom.yml up -d --force-recreate
```

**旧版（`docker-compose` 带连字符，若报 unknown shorthand flag 'f' 用这条）：**
```bash
cd ~/nofx && docker-compose -f docker-compose.custom.yml build --no-cache && docker-compose -f docker-compose.custom.yml up -d --force-recreate
```

或先 pull 再构建重启（任选上面两种写法之一，把 `cd ~/nofx &&` 换成 `cd ~/nofx && git pull origin my-custom-version &&` 即可）。

## 说明

- 编译在 Docker 镜像内完成，**不需要**在服务器上安装 Go、npm。
- 若提示 `docker` 或 `docker compose` 未找到，请先执行方式一完成首次安装（脚本会安装 Docker 并构建运行）。
- 更多安装与迁移说明见项目根目录 **install-custom.sh** 顶部注释。

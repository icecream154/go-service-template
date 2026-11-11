# Go 服务部署

同一套 Compose 启动 Go 应用和日志 Agent。Go Logger 根据环境 YAML 中的 `file_path` 将 JSON 日志追加到共享卷的 `app.jsonl`，并继续输出到 stdout。Agent 读取文件并保存采集进度，再通过 OTLP HTTP 发往该环境的中心 Collector。中心写入 ClickHouse，HyperDX 从 `otel.otel_logs` 查询。

## 目录与配置

| 文件 | 用途 |
| --- | --- |
| `Dockerfile`、`Dockerfile.dockerignore` | 从仓库根目录构建 `go-service` 二进制和镜像，应用配置在部署时挂载 |
| `compose.yaml` | 公共的 `app`、`log-agent`、配置挂载、日志卷和采集进度卷 |
| `collector/otelcol-go-logs.yaml` | 三套环境共用的日志采集与转发配置 |
| `environments/<env>/compose.yaml` | 固定 `APP_ENV`，选择本地构建或已发布镜像 |
| `environments/<env>/.env` | 纳入 Git 的环境部署参数，启动脚本直接读取 |
| `compose.sh` | 选择 dev、test 或 prod 的配置并转交 Compose 命令 |

`.env` 保存宿主机监听地址和端口、Collector 地址，以及 test 和 prod 使用的 `GO_APP_IMAGE`。仓库里的 `OTLP_AUTH_TOKEN` 留空，由启动终端或部署系统注入同名环境变量，供 `log-agent` 使用。

三套环境均使用 `GO_BIND_ADDRESS=0.0.0.0`，将 `GO_HTTP_PORT` 发布到宿主机的所有 IPv4 网卡。客户端通过宿主机实际可达的 IP 或域名和该端口访问，容器内部统一使用 `8080`。跨机器访问还取决于路由、防火墙和云安全组的放行规则。

公共 `deploy/compose.yaml` 直接将 `../config` 只读挂载到 `/app/config`。`../config` 相对于 `deploy/`，指向项目根目录的 `config/`。三套环境共用这个目录，Go 根据 `APP_ENV` 选择 `dev.yml`、`test.yml` 或 `prod.yml`。部署时把所选环境 YAML 中的 `${NAME}` 占位替换为实际值。

启动脚本加载公共 Compose 和一个环境覆盖文件。每个环境覆盖文件的 `name` 按「项目名-环境」设置，分别是 `go-service-template-dev`、`go-service-template-test` 和 `go-service-template-prod`，用来隔离容器、网络及卷。同一台机器上部署多个服务时，可在对应 `.env` 设置 `COMPOSE_PROJECT_NAME`，并为它们选择各自的 `GO_HTTP_PORT`。Compose 中的相对挂载路径以 `deploy/` 为基准。使用外部 `.env` 时，可通过 `DEPLOY_ENV_FILE` 指定绝对路径。终端已导出的同名变量优先于 `.env` 中的值。

## 日志文件与服务名

Go 的工作目录为 `/app`。环境配置 `logger/<env>.yml` 中的 `file_path: .data/logs/app.jsonl` 会写入 `/app/.data/logs/app.jsonl`。Compose 将 `go-logs` 卷挂到两个容器的 `/app/.data/logs`，Agent 只读挂载，`include` 指向同一个文件。三个环境的仓库模板已包含 `file_path`，以前复制的私有配置需要在部署时补上这一项。省略 `file_path` 时，Go 将日志写到 stdout。

`filelog/go-service` 是接收器的实例名，供 `service.pipelines` 下的 `receivers` 引用。`logs/go-service` 是日志管道名。HyperDX 的 `ServiceName` 来自 Go 的 `logger/<env>.yml` 中的 `service_name`。Go 将该值写入每条 JSON，Agent 将它映射到 OpenTelemetry 的 `service.name`。

## 本地 dev

1. 启动前在终端提供与中心 Collector 相同的 `OTLP_AUTH_TOKEN`。默认的 `GO_LOG_OTLP_ENDPOINT=http://host.docker.internal:4318` 适合同一台 Mac 上的 Docker Desktop 与已发布 4318 端口的中心。其他部署填写 Agent 可达的 OTLP HTTP 基础地址，Agent 会追加 `/v1/logs`。

2. 在项目根目录的 `config/` 里配置业务依赖。使用 Docker Desktop 连接 Mac 上的 MySQL 时，将 `config/database/mysql/dev.yml` 的 `Write.Host` 配为 `host.docker.internal`，沿用本机 MySQL 的端口、账号和密码。这个宿主机地址在 dev 环境允许 `Write.TLS.Enabled: false`。直接用 `go run` 在 Mac 上启动时，可使用 `127.0.0.1`。配置 Python 客户端时，可在 `config/integrations/company/dev.yml` 中使用 `http://host.docker.internal:8000` 访问 Mac 上的服务。

3. 确认中心服务和 MySQL 可用。在仓库根目录启动 Go 和 Agent。

   ```sh
   ./deploy/compose.sh dev up -d --build
   ```

4. 正常访问 Go 接口后，在 HyperDX 的 **Search → Logs** 选择最近 15 分钟，搜索 `ServiceName:"<service_name>"`。用当前应用 YAML 中的 `service_name` 替换占位符。用 `./deploy/compose.sh dev logs app log-agent` 查看应用 JSON 日志、stderr 和 Agent 自身的日志。

如果以其他 Compose 项目名启动的容器还占用端口，先停止那些容器。日志卷和进度卷按 Compose 项目名管理。使用曾经用过的项目名会复用该项目仍存在的卷。

## test 和 prod

1. 在 `environments/test/.env` 或 `environments/prod/.env` 设置该环境的部署参数。在项目根目录的 `config/` 中，将所选环境 YAML 的数据库和服务地址等占位填写为实际值，并提供其引用的 CA 证书。在 `.env` 填写 `GO_LOG_OTLP_ENDPOINT`，并由启动环境提供对应的 `OTLP_AUTH_TOKEN`。
2. 在发布流程中构建并推送 Go 镜像，将候选镜像 digest 填入 test 的 `GO_APP_IMAGE`。test 验证通过后，将同一 digest 填入 prod 的 `GO_APP_IMAGE`。

   ```sh
   ./deploy/compose.sh test up -d
   ./deploy/compose.sh prod up -d
   ```

3. 回滚时将目标环境的 `GO_APP_IMAGE` 切回上一版 digest，使用与该镜像匹配的配置，再用对应环境运行 `up -d`。例如回滚到尚不支持 `file_path` 的镜像时，使用其对应版本的日志配置。

应用日志和 Agent 读取位置分别保存在该 Compose 项目的 `go-logs` 与 `log-agent-state` 卷中。ClickHouse 的表 TTL 管理已入库日志，本地日志文件继续写在日志卷里。

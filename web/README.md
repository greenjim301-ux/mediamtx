# MediaMTX 视频源管理页面

React + TypeScript + Vite + Tailwind CSS，使用 shadcn/ui 风格的 Radix 组件。

## 部署与托管

推荐由 MediaMTX 自身托管。构建后的页面嵌入 Go 可执行文件，与 Control API
共用监听端口。生产环境不需要 Node.js、独立前端服务或 CDN。

```sh
make
# 首次运行时复制配置；已有本地配置不覆盖。
cp -n mediamtx.yml mediamtx-local.yml
./binaries/mediamtx mediamtx-local.yml
```

在项目根目录运行 `make`（或 `make build`）即可构建前后端，输出
`binaries/mediamtx`。构建环境须安装 Go（版本见 `go.mod`）、Node.js 及 npm
（推荐 Node.js 24）。首次构建或前端依赖清单变化时自动执行 `npm ci`；
每次构建都会生成 Go 所需资源、重新构建前端，再将页面嵌入 Go 程序。
仅构建前端可运行 `make frontend`，查看其他命令可运行 `make help`。

`mediamtx.yml` 是版本管理中的默认配置模板。管理页面会改写启动时指定的
配置文件（包括重新排列 YAML 字段和注释），因此日常运行推荐使用
`mediamtx-local.yml`。该文件及 `mediamtx-local*.yml` 备份不纳入 Git，
页面创建的源和服务器配置保留在本地；不要将运行时生成的服务器路径、设备信息
或密码提交到默认模板。已有本地配置时直接使用，不需要再次复制。

默认访问：`http://服务器IP:9997/admin/`。

配置须启用：

```yaml
api: true
apiAddress: :9997
httpflv: true
httpflvAddress: :8891
apiFileUploadDirectory: ./uploads
apiFileUploadMaxSize: 2G
```

已将构建产物放在 `internal/api/webdist/`，因此普通 Go 构建可直接提供页面。
修改前端后须重新构建前端和 Go 可执行文件。这里使用的播放器是从
`/home/lisi/Documents/work/mpegts.js/dist/mpegts.js` 复制的版本，位于
`web/public/vendor/mpegts.js`；修改播放器后需更新该副本并重新构建。
附带其 Apache-2.0 许可证。

默认配置只允许本机匿名访问 Control API。远程管理须在 `authInternalUsers`
中配置带 `api` 权限的用户；页面预览还需要 `read` 权限。例如：

```yaml
authInternalUsers:
  - user: admin
    pass: YOUR_PASSWORD
    permissions:
      - action: api
      - action: read
  - user: any
    permissions:
      - action: publish
```

这是可选的鉴权示例，不会由页面自动修改用户或权限。生产环境可配置 API HTTPS，
或在 Nginx/Caddy 后面使用 HTTPS。反向代理需保留 `/admin/` 和 `/v3/` 路径；
FLV 预览路由 `/v3/preview/` 需关闭代理缓冲并允许长时间流式响应。

静态页面可公开加载，但不含设备或配置数据；Control API、持久配置操作仍受
`api` 权限保护，预览还会检查该路径的 `read` 权限。页面连接凭据保存在当前
标签页的 sessionStorage 中，不放在 URL 或配置文件里。

## 功能

- **RTSP 源**：列出已配置的 RTSP/RTSPS 输入，添加、删除，查看运行状态和客户端数，
  HTTP-FLV 预览。新建 RTSP 源使用 TCP 接入。
- **文件源**：从浏览器上传 MP4/MKV 视频，查看上传进度并支持取消；服务端校验
  视频后自动创建持久文件源。支持 H.264/H.265，忽略音频，默认持续循环并共享播放进度。
  删除上传文件源时，同时删除对应上传视频。通过 YAML 手工配置的文件源仅删除配置，
  不删除它们引用的外部文件。页面删除确认会说明实际行为。
- **国标源**：配置 GB28181 上级平台的启用状态、SIP 编码/域/监听、传输方式、
  媒体 IP 和端口池、心跳与路径模板；列出注册设备、同步目录、查看通道并预览。
  通道已被显式路径或国标正则规则覆盖时直接预览，不新增配置；
  未覆盖的通道通过“接入并预览”创建持久化、按需拉流的 `gb28181://` 输入。
- 搜索、分页数据自动加载、每 10 秒刷新、加载/错误/空列表状态、删除确认。
- H.265 预览需要播放器及浏览器支持 HEVC；G.711 音频的浏览器兼容性取决于播放器。

## 国标摄像头识别

分别显示注册设备/下级平台和目录通道，不将所有目录节点当作摄像头。

- 20 位国标 ID 的第 11–13 位为 `131` 或 `132` 时识别为摄像头。
- `111`、`118` 识别为录像设备，`200` 为下级平台，`215`、`216` 为目录节点。
- 编码不规范的直接注册设备，可参考其上报的 IPC/Camera/摄像机类型。
- 其他节点标记为“待识别”，可在“全部目录节点”中人工核对并尝试接入。
  不会将父设备的摄像机类型自动套用给所有子节点。

仓库配置默认提供按需拉流规则：

```yaml
paths:
  ~^gb28181/(.+)$:
    source: gb28181://$G1
    sourceOnDemand: yes
  all_others:
```

使用此规则仍需启用国标服务并获取下级设备目录。规则本身不会主动拉取所有通道。
后端按实际路径匹配顺序（显式路径优先、正则规则按顺序匹配、兜底最后）判断通道是否
已配置，并通过目录接口的 `pathConfigured`、`pathConflict`、`pathConfigName` 字段返回。
页面不在 JavaScript 中重新解释 Go 正则，避免不同正则引擎和匹配优先级造成误判。
路径实际指向其他源或其他国标通道时，显示“路径冲突”并阻止预览。

通道流路径采用服务端 `gb28181PathTemplate` 返回的 `path`。
更改模板后，已创建的显式通道路径不会自动迁移；应先检查已有配置及路径冲突。

## 上传文件存储

`apiFileUploadDirectory` 指定上传目录，默认 `./uploads`。相对路径以配置文件所在
目录为基准，也可指定绝对路径。文件源页的“上传设置”可以修改目录和单文件大小上限，
并持久写入配置文件。`apiFileUploadMaxSize` 默认 `2G`，可设置为 `500M` 等大小。

文件以服务端生成的唯一名称保存，避免路径穿越和同名覆盖；原始文件名用于页面显示。
服务用户需有目录创建和文件读写权限。请求流式写盘，上传失败、取消、超过上限、
视频不受支持或配置保存失败时清理文件。只接收一份文件，不接受额外表单字段。

每个上传文件源在路径配置中记录 `fileUploadDirectory` 与 `fileUploadOriginalName`。
修改上传目录不会移动已有文件；这些记录保证旧文件源仍能从原目录删除对应文件。
删除时只处理记录目录内具有上传命名格式的普通文件，不跟随文件符号链接；被其他
文件源引用的文件会拒绝删除。删除失败时尝试恢复文件与原配置，并返回错误。

反向代理部署需同步调整上传大小限制（例如 Nginx `client_max_body_size`）及读写超时。
服务端按上传读取进度刷新读取超时，避免持续传输的大文件被普通 API 总读取时长限制。

## 配置持久化

现有 `/v3/config/...` 接口继续保持运行时修改语义。本页面使用新增管理接口：

- `GET /v3/management/info`：查询配置文件路径与持久化能力。
- `POST /v3/management/apply`：添加/删除源、修改国标全局配置或上传存储设置。
- `POST /v3/management/files/upload/<path>`：使用 multipart 的 `file` 字段上传并创建文件源。
- `GET /v3/preview/<path>.flv`：同源预览，复用真实 HTTP-FLV 会话与读取鉴权。

持久化操作在 Core 中串行执行，读取当前配置文件，只修改明确提交的字段，
保留其他配置与已有密码，正常情况下保留 YAML 注释（排版与键顺序可能调整）。
先完整校验，再在同目录写入临时文件并原子替换，随后热加载。
不支持直接编辑加密配置文件；只读文件或未加载配置文件时保存会失败。
服务用户需要对配置文件及其所在目录有写权限。

配置文件是持久配置的来源。环境变量仍具有正常的覆盖优先级；不要同时通过
环境变量管理同一源或字段。此前用普通 Control API 做的运行时修改，若未写入
文件，可能在本次文件配置热加载时被文件内容覆盖。

修改国标全局配置可能重建服务并中断现有连接。页面会重试刷新状态，但不会自动
重发写操作；监听地址冲突等运行期错误需检查 MediaMTX 日志。

## 本地开发

先启动更新后的 MediaMTX，再运行：

```sh
cd web
npm ci
npm run dev
```

打开 `http://127.0.0.1:5173/admin/`。Vite 默认把 `/v3/` 转发到
`http://127.0.0.1:9997`，也可指定：

```sh
MEDIAMTX_API_URL=http://127.0.0.1:21997 npm run dev
```

Vite 仅用于开发；生产环境优先使用上面的 Go 内嵌托管方式。

## 验证

```sh
npm test
npm run build
cd ..
go test ./internal/api ./internal/conf ./internal/servers/httpflv
go test -race ./internal/core -run TestManagement -count=1
```

后端覆盖源增删的持久化与冷重启、无效配置不写入、密码与注释保留、
API 鉴权、视频读取权限、静态托管、真实文件流同源预览，以及国标服务启停。
上传覆盖大小限制、视频校验、取消与失败清理、目录修改后删除、共享文件与符号链接保护。
识别与 URL 转换由前端单元测试覆盖。真实下级国标平台和摄像头的目录/媒体互通
仍需在部署环境联调。

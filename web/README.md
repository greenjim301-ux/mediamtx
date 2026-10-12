# MediaMTX 视频源管理页面

React + TypeScript + Vite + Tailwind CSS，使用 shadcn/ui 风格的 Radix 组件。

## 部署与托管

推荐由 MediaMTX 自身托管。构建后的页面嵌入 Go 可执行文件，与 Control API
共用监听端口。生产环境不需要 Node.js、独立前端服务或 CDN。

```sh
cd web
npm ci
npm run build
cd ..
go build -o binaries/mediamtx .
./binaries/mediamtx mediamtx.yml
```

默认访问：`http://服务器IP:9997/admin/`。

配置须启用：

```yaml
api: true
apiAddress: :9997
httpflv: true
httpflvAddress: :8891
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
- **文件源**：添加服务器上的 MP4/MKV 文件路径，删除源配置，HTTP-FLV 预览。
  文件须预先放到服务器，页面不上传文件；删除配置不会删除视频文件。
  支持 H.264/H.265，忽略音频，默认持续循环并共享播放进度。
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

## 配置持久化

现有 `/v3/config/...` 接口继续保持运行时修改语义。本页面使用新增管理接口：

- `GET /v3/management/info`：查询配置文件路径与持久化能力。
- `POST /v3/management/apply`：添加/删除源或修改国标全局配置。
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
识别与 URL 转换由前端单元测试覆盖。真实下级国标平台和摄像头的目录/媒体互通
仍需在部署环境联调。

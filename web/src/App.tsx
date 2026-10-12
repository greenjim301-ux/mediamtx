import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type FormEvent,
  type ReactNode,
} from "react";
import {
  Activity,
  ArrowUpRight,
  Camera,
  Check,
  CheckCircle2,
  ChevronRight,
  Database,
  FileVideo,
  FolderOpen,
  Globe2,
  HardDrive,
  Loader2,
  LockKeyhole,
  Network,
  Plus,
  Radio,
  RefreshCw,
  Search,
  Server,
  Settings2,
  ShieldCheck,
  Trash2,
  Video,
  Wifi,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { Preview } from "@/components/preview";
import {
  apply,
  fileSource,
  hideCredentials,
  request,
  setAuthorization,
  snapshot,
  type Channel,
  type GlobalConfig,
  type PathConf,
  type Snapshot,
} from "@/lib/api";
import {
  classify,
  classifyChannel,
  kindLabels,
  channelPreviewAction,
} from "@/lib/devices";

type Page = "rtsp" | "file" | "gb";
const pages = {
  rtsp: {
    title: "RTSP 源",
    description: "接入摄像头或其他服务的 RTSP 视频流，统一调阅与预览。",
    icon: Radio,
  },
  file: {
    title: "文件源",
    description: "将服务器上的视频文件作为持续循环的视频源。",
    icon: FileVideo,
  },
  gb: {
    title: "国标源",
    description: "管理 GB28181 上级平台，发现下级设备与摄像头通道。",
    icon: Network,
  },
};
function initialPage(): Page {
  const p = location.hash.slice(1);
  return p === "file" || p === "gb" ? p : "rtsp";
}
function Field({
  label,
  children,
  hint,
}: {
  label: string;
  children: ReactNode;
  hint?: string;
}) {
  return (
    <label className="block space-y-2 text-sm">
      <span className="font-medium text-slate-700">{label}</span>
      {children}
      {hint && (
        <span className="block text-xs leading-relaxed text-muted-foreground">
          {hint}
        </span>
      )}
    </label>
  );
}
function Badge({ online, label }: { online?: boolean; label?: string }) {
  return (
    <span
      className={`badge ${online === undefined ? "badge-neutral" : online ? "badge-green" : "badge-gray"}`}
    >
      <span className="size-1.5 rounded-full bg-current" />
      {label || (online ? "在线" : "离线")}
    </span>
  );
}
function Empty({
  icon: Icon,
  title,
  description,
  action,
}: {
  icon: typeof Radio;
  title: string;
  description: string;
  action?: ReactNode;
}) {
  return (
    <div className="flex min-h-64 flex-col items-center justify-center px-6 py-10 text-center">
      <div className="mb-4 rounded-2xl border bg-slate-50 p-4">
        <Icon className="text-slate-400" size={26} />
      </div>
      <h3 className="font-semibold text-slate-700">{title}</h3>
      <p className="mt-2 max-w-md text-sm leading-6 text-muted-foreground">
        {description}
      </p>
      {action && <div className="mt-5">{action}</div>}
    </div>
  );
}
function Metric({
  label,
  value,
  detail,
  icon: Icon,
}: {
  label: string;
  value: number | string;
  detail: string;
  icon: typeof Radio;
}) {
  return (
    <div className="metric">
      <div>
        <p className="text-xs font-medium text-muted-foreground">{label}</p>
        <p className="mt-2 text-3xl font-semibold tracking-tight">{value}</p>
        <p className="mt-2 text-xs text-muted-foreground">{detail}</p>
      </div>
      <div className="rounded-xl bg-slate-50 p-3">
        <Icon size={19} className="text-slate-500" />
      </div>
    </div>
  );
}

export default function App() {
  const [page, setPage] = useState<Page>(initialPage);
  const [data, setData] = useState<Snapshot | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [query, setQuery] = useState("");
  const [adding, setAdding] = useState(false);
  const [deleting, setDeleting] = useState<PathConf | null>(null);
  const [busy, setBusy] = useState(false);
  const [preview, setPreview] = useState<string | null>(null);
  const [connection, setConnection] = useState(false);
  const [gbSettings, setGBSettings] = useState(false);
  const [cameraOnly, setCameraOnly] = useState(true);
  const [selectedDevice, setSelectedDevice] = useState("");
  const refresh = useCallback(async (showLoading = false) => {
    if (showLoading) setLoading(true);
    try {
      const next = await snapshot();
      setData(next);
      setError("");
      return next;
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      throw e;
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => {
    void refresh().catch(() => {});
    const interval = setInterval(() => {
      if (!document.hidden) void refresh().catch(() => {});
    }, 10000);
    return () => clearInterval(interval);
  }, [refresh]);
  useEffect(() => {
    const change = () => {
      setPage(initialPage());
      setQuery("");
    };
    window.addEventListener("hashchange", change);
    return () => window.removeEventListener("hashchange", change);
  }, []);
  const save = async (change: unknown, message: string) => {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await apply(change);
      setNotice(message);
      let refreshed = false;
      for (let i = 0; i < 8; i++) {
        await new Promise((r) => setTimeout(r, 300));
        try {
          const next = await snapshot();
          setData(next);
          setError("");
          refreshed = true;
          break;
        } catch {
          /* service can briefly restart after a global setting change */
        }
      }
      if (!refreshed)
        setError("配置已写入，暂时无法连接服务；请检查服务日志并刷新。");
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setError(msg);
      throw e;
    } finally {
      setBusy(false);
    }
  };
  const sourceRows = useMemo(
    () =>
      data?.configs
        .filter((c) =>
          page === "rtsp"
            ? /^rtsps?:\/\//.test(c.source)
            : page === "file"
              ? c.source.startsWith("file://")
              : false,
        )
        .filter((c) =>
          (c.name + " " + hideCredentials(c.source))
            .toLowerCase()
            .includes(query.toLowerCase()),
        ) || [],
    [data, page, query],
  );
  const allSources =
    data?.configs.filter(
      (c) => /^rtsps?:\/\//.test(c.source) || c.source.startsWith("file://"),
    ) || [];
  const currentSources = allSources.filter((c) =>
    page === "rtsp"
      ? /^rtsps?:\/\//.test(c.source)
      : c.source.startsWith("file://"),
  );
  const runtime = (name: string) => data?.paths.find((p) => p.name === name);
  const cameras =
    data?.channels.filter(
      (c) => classifyChannel(c, data.devices).kind === "camera",
    ) || [];
  const channels = (data?.channels || [])
    .filter(
      (c) =>
        !cameraOnly ||
        classifyChannel(c, data?.devices || []).kind === "camera",
    )
    .filter((c) => !selectedDevice || c.deviceId === selectedDevice)
    .filter((c) =>
      (c.name + " " + c.id + " " + c.deviceId)
        .toLowerCase()
        .includes(query.toLowerCase()),
    );
  const nationalPreview = async (c: Channel) => {
    const action = channelPreviewAction(c);
    if (action === "conflict") {
      setError("该通道的流路径已被其他视频源占用，请修改国标路径模板。");
      return;
    }
    try {
      if (action === "add")
        await save(
          {
            action: "add",
            name: c.path,
            path: {
              source: `gb28181://${c.deviceId}/${c.id}`,
              sourceOnDemand: true,
            },
          },
          "通道已接入并保存。",
        );
      setPreview(c.path);
    } catch {
      /* error shown above */
    }
  };
  const canEdit = !!data?.management.persistent && !busy;
  const Icon = pages[page].icon;
  return (
    <div className="app-shell">
      <aside className="sidebar">
        <a href="#rtsp" className="brand">
          <span className="brand-mark">
            <Radio size={22} />
          </span>
          <span>
            <strong>MediaMTX</strong>
            <small>视频服务管理</small>
          </span>
        </a>
        <div className="nav-label">视频源管理</div>
        <nav aria-label="主导航">
          {(Object.keys(pages) as Page[]).map((key) => {
            const I = pages[key].icon;
            return (
              <a
                key={key}
                href={"#" + key}
                className={"nav-link " + (page === key ? "active" : "")}
              >
                <I size={18} />
                <span>{pages[key].title}</span>
                {page === key && <ChevronRight size={15} className="ml-auto" />}
              </a>
            );
          })}
        </nav>
        <div className="sidebar-bottom">
          <div className="flex items-center gap-2 text-xs font-medium">
            <span
              className={
                "size-2 rounded-full " +
                (data && !error ? "bg-emerald-500" : "bg-slate-300")
              }
            />
            {data && !error ? "服务已连接" : "等待连接"}
          </div>
          <p className="mt-2 text-[11px] text-slate-400">
            Control API · 同源管理
          </p>
          <button
            className="mt-4 flex items-center gap-2 text-xs text-slate-500 hover:text-slate-900"
            onClick={() => setConnection(true)}
          >
            <LockKeyhole size={14} />
            连接凭据
          </button>
        </div>
      </aside>
      <div className="main">
        <header className="topbar">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Server size={15} />
            <span>控制台</span>
            <ChevronRight size={13} />
            <span className="text-slate-700">{pages[page].title}</span>
          </div>
          <div className="flex items-center gap-3">
            <span className="hidden text-xs text-muted-foreground sm:block">
              每 10 秒自动刷新
            </span>
            <Button
              variant="ghost"
              size="icon"
              aria-label="刷新"
              disabled={loading || busy}
              onClick={() => void refresh(true).catch(() => {})}
            >
              <RefreshCw className={loading ? "animate-spin" : ""} />
            </Button>
            <button
              className="avatar"
              onClick={() => setConnection(true)}
              aria-label="连接凭据"
            >
              MT
            </button>
          </div>
        </header>
        <main className="content">
          <div className="page-heading">
            <div>
              <div className="mb-2 flex items-center gap-2 text-[11px] font-semibold uppercase tracking-[.18em] text-slate-400">
                <Icon size={14} />
                MEDIA SOURCES
              </div>
              <h1>{pages[page].title}</h1>
              <p>{pages[page].description}</p>
            </div>
            {page === "gb" ? (
              <Button
                variant="outline"
                disabled={!data}
                onClick={() => setGBSettings(true)}
              >
                <Settings2 />
                国标服务配置
              </Button>
            ) : (
              <Button disabled={!canEdit} onClick={() => setAdding(true)}>
                <Plus />
                添加{page === "rtsp" ? " RTSP 源" : "文件源"}
              </Button>
            )}
          </div>
          {error && (
            <div role="alert" className="banner banner-error">
              <span>{error}</span>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setConnection(true)}
              >
                连接设置
              </Button>
            </div>
          )}
          {notice && (
            <div role="status" className="banner banner-success">
              <span className="flex items-center gap-2">
                <CheckCircle2 size={16} />
                {notice}
              </span>
              <button aria-label="关闭提示" onClick={() => setNotice("")}>
                <X size={16} />
              </button>
            </div>
          )}
          {data && !data.management.persistent && (
            <div className="banner banner-warning">
              服务未加载配置文件，无法持久保存。请使用配置文件启动 MediaMTX。
            </div>
          )}
          <div className="metrics-grid">
            {page === "gb" ? (
              <>
                <Metric
                  label="注册设备 / 平台"
                  value={data?.devices.length || 0}
                  detail="包含摄像机、录像设备及下级平台"
                  icon={Server}
                />
                <Metric
                  label="已识别摄像头"
                  value={cameras.length}
                  detail={`目录总计 ${data?.channels.length || 0} 个节点`}
                  icon={Camera}
                />
                <Metric
                  label="在线摄像头"
                  value={cameras.filter((c) => c.online).length}
                  detail="由下级设备上报目录状态"
                  icon={Wifi}
                />
              </>
            ) : (
              <>
                <Metric
                  label={page === "rtsp" ? "RTSP 视频源" : "文件播放通道"}
                  value={currentSources.length}
                  detail={
                    page === "rtsp"
                      ? "已保存的接入配置"
                      : "MP4 / MKV · 忽略音频"
                  }
                  icon={Icon}
                />
                <Metric
                  label="运行中的源"
                  value={
                    currentSources.filter((c) => runtime(c.name)?.online).length
                  }
                  detail="已接入并产生视频流"
                  icon={Activity}
                />
                <Metric
                  label="调阅连接"
                  value={currentSources.reduce(
                    (n, c) => n + (runtime(c.name)?.readers?.length || 0),
                    0,
                  )}
                  detail="当前路径上的输出客户端"
                  icon={Video}
                />
              </>
            )}
          </div>
          {page === "gb" && (
            <>
              <section className="service-card">
                <div className="flex items-center gap-3">
                  <span className="rounded-xl bg-blue-50 p-3 text-blue-600">
                    <Globe2 size={22} />
                  </span>
                  <div>
                    <h2>GB28181 上级平台</h2>
                    <p className="mt-1 text-xs text-muted-foreground">
                      {data?.global.gb28181Serial || "尚未连接服务"}
                    </p>
                  </div>
                </div>
                <div className="service-values">
                  <div>
                    <small>SIP 监听</small>
                    <strong>{data?.global.gb28181Address || "—"}</strong>
                  </div>
                  <div>
                    <small>媒体传输</small>
                    <strong>
                      {data?.global.gb28181MediaProtocol?.toUpperCase() || "—"}
                    </strong>
                  </div>
                  <Badge
                    online={data?.global.gb28181}
                    label={data?.global.gb28181 ? "服务已启用" : "服务未启用"}
                  />
                </div>
              </section>
              <section className="panel mb-5">
                <div className="panel-header">
                  <div>
                    <h2>接入设备</h2>
                    <p>选择设备查看它的目录通道</p>
                  </div>
                  {selectedDevice && (
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => setSelectedDevice("")}
                    >
                      显示全部设备
                    </Button>
                  )}
                </div>
                {!data?.devices.length ? (
                  <Empty
                    icon={Server}
                    title="暂无注册设备"
                    description={
                      data?.global.gb28181
                        ? "在下级平台配置本服务的 SIP 信息，注册成功后设备会出现在这里。"
                        : "启用国标服务并配置 SIP 信息后，可接入下级平台。"
                    }
                  />
                ) : (
                  <div className="device-grid">
                    {data.devices.map((d) => {
                      const info = classify(d.id, d.deviceType);
                      return (
                        <div
                          key={d.id}
                          className={
                            "device-card " +
                            (selectedDevice === d.id ? "selected" : "")
                          }
                        >
                          <button
                            className="w-full text-left"
                            onClick={() =>
                              setSelectedDevice(
                                selectedDevice === d.id ? "" : d.id,
                              )
                            }
                          >
                            <div className="flex items-center justify-between gap-2">
                              <span className="flex items-center gap-2 font-medium">
                                <Server size={16} className="text-slate-400" />
                                {d.name || kindLabels[info.kind]}
                              </span>
                              <Badge online={d.online} />
                            </div>
                            <p className="mt-3 font-mono text-[11px] text-slate-500">
                              {d.id}
                            </p>
                            <p
                              className="mt-2 text-xs text-muted-foreground"
                              title={info.evidence}
                            >
                              {kindLabels[info.kind]} ·{" "}
                              {d.channels?.length || 0} 个目录节点
                            </p>
                          </button>
                          <div className="mt-3 flex items-center justify-between border-t pt-3">
                            <span className="truncate text-[11px] text-muted-foreground">
                              {d.address}
                            </span>
                            <Button
                              variant="ghost"
                              size="sm"
                              disabled={busy || !d.online}
                              onClick={async () => {
                                setBusy(true);
                                try {
                                  await request(
                                    "/gb28181/devices/refresh/" +
                                      encodeURIComponent(d.id),
                                    { method: "POST" },
                                  );
                                  setNotice(
                                    "已发送目录查询，等待下级设备返回；列表会自动更新。",
                                  );
                                } catch (e) {
                                  setError(String(e));
                                } finally {
                                  setBusy(false);
                                }
                              }}
                            >
                              <RefreshCw />
                              同步目录
                            </Button>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                )}
              </section>
            </>
          )}
          <section className="panel">
            <div className="panel-header">
              <div>
                <h2>
                  {page === "gb"
                    ? "摄像头与目录通道"
                    : page === "rtsp"
                      ? "RTSP 源列表"
                      : "文件源列表"}
                  <span className="count">
                    {page === "gb" ? channels.length : sourceRows.length}
                  </span>
                </h2>
                <p>
                  {page === "gb"
                    ? "按国标编码与上报类型识别，未知节点保留供人工核对。"
                    : "配置持久保存，重启服务后继续生效。"}
                </p>
              </div>
              <div className="filters">
                {page === "gb" && (
                  <select
                    aria-label="通道筛选"
                    value={cameraOnly ? "camera" : "all"}
                    onChange={(e) => setCameraOnly(e.target.value === "camera")}
                  >
                    <option value="camera">只看摄像头</option>
                    <option value="all">全部目录节点</option>
                  </select>
                )}
                <div className="search">
                  <Search size={16} />
                  <Input
                    aria-label="搜索视频源"
                    placeholder={
                      page === "gb" ? "搜索名称或设备 ID" : "搜索名称或源地址"
                    }
                    value={query}
                    onChange={(e) => setQuery(e.target.value)}
                  />
                </div>
              </div>
            </div>
            {loading && !data ? (
              <div className="flex min-h-64 items-center justify-center gap-2 text-sm text-muted-foreground">
                <Loader2 className="animate-spin" size={18} />
                正在连接服务…
              </div>
            ) : page === "gb" ? (
              channels.length ? (
                <div className="table-scroll">
                  <table>
                    <thead>
                      <tr>
                        <th>通道名称 / 编码</th>
                        <th>识别类型</th>
                        <th>所属设备</th>
                        <th>目录状态</th>
                        <th className="text-right">操作</th>
                      </tr>
                    </thead>
                    <tbody>
                      {channels.map((c) => {
                        const info = classifyChannel(c, data?.devices || []);
                        const previewAction = channelPreviewAction(c);
                        const configured = previewAction === "preview";
                        const playable =
                          info.kind === "camera" || info.kind === "unknown";
                        return (
                          <tr key={c.deviceId + ":" + c.id}>
                            <td>
                              <div className="source-cell">
                                <span className="source-icon">
                                  <Camera size={18} />
                                </span>
                                <div>
                                  <strong>{c.name || "未命名通道"}</strong>
                                  <small className="font-mono">{c.id}</small>
                                </div>
                              </div>
                            </td>
                            <td>
                              <span
                                title={info.evidence}
                                className={
                                  "type-pill " +
                                  (info.kind === "camera" ? "type-blue" : "")
                                }
                              >
                                {kindLabels[info.kind]}
                              </span>
                            </td>
                            <td>
                              <div className="text-xs">
                                {data?.devices.find((d) => d.id === c.deviceId)
                                  ?.name || "注册设备"}
                              </div>
                              <small className="font-mono text-[10px] text-muted-foreground">
                                {c.deviceId}
                              </small>
                            </td>
                            <td>
                              <Badge online={c.online} />
                            </td>
                            <td>
                              <div className="row-actions">
                                {playable && (
                                  <Button
                                    variant="outline"
                                    size="sm"
                                    disabled={
                                      !c.online ||
                                      c.pathConflict ||
                                      busy ||
                                      !data?.global.httpflv ||
                                      (!configured && !canEdit)
                                    }
                                    onClick={() => void nationalPreview(c)}
                                  >
                                    <Video />
                                    {c.pathConflict
                                      ? "路径冲突"
                                      : configured
                                        ? "预览"
                                        : info.kind === "unknown"
                                          ? "尝试接入"
                                          : "接入并预览"}
                                  </Button>
                                )}
                              </div>
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              ) : (
                <Empty
                  icon={Camera}
                  title={
                    query
                      ? "没有匹配的通道"
                      : cameraOnly
                        ? "暂无已识别的摄像头"
                        : "暂无目录通道"
                  }
                  description={
                    cameraOnly
                      ? "可切换到“全部目录节点”检查未知编码，或对已注册设备同步目录。"
                      : "下级平台返回目录后，通道会自动显示。"
                  }
                />
              )
            ) : sourceRows.length ? (
              <div className="table-scroll">
                <table>
                  <thead>
                    <tr>
                      <th>名称 / 流路径</th>
                      <th>{page === "rtsp" ? "源地址" : "服务器文件路径"}</th>
                      <th>状态</th>
                      <th>调阅连接</th>
                      <th className="text-right">操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    {sourceRows.map((c) => {
                      const live = runtime(c.name);
                      return (
                        <tr key={c.name}>
                          <td>
                            <div className="source-cell">
                              <span className="source-icon">
                                <Icon size={18} />
                              </span>
                              <div>
                                <strong>{c.name.split("/").at(-1)}</strong>
                                <small className="font-mono">{c.name}</small>
                              </div>
                            </div>
                          </td>
                          <td>
                            <div
                              className="source-address"
                              title={hideCredentials(c.source)}
                            >
                              {hideCredentials(c.source)}
                            </div>
                            <small className="text-[11px] text-muted-foreground">
                              {page === "file"
                                ? "无限循环 · 仅视频"
                                : c.rtspTransport === "tcp"
                                  ? "TCP 传输"
                                  : "自动选择传输"}
                              {c.sourceOnDemand ? " · 按需接入" : ""}
                            </small>
                          </td>
                          <td>
                            <Badge
                              online={!!live?.online}
                              label={
                                live?.online
                                  ? "运行中"
                                  : c.sourceOnDemand
                                    ? "等待调阅"
                                    : "未就绪"
                              }
                            />
                          </td>
                          <td className="font-mono text-sm">
                            {live?.readers?.length || 0}
                          </td>
                          <td>
                            <div className="row-actions">
                              <Button
                                variant="outline"
                                size="sm"
                                disabled={!data?.global.httpflv}
                                onClick={() => setPreview(c.name)}
                              >
                                <Video />
                                预览
                              </Button>
                              <Button
                                variant="ghost"
                                size="icon"
                                aria-label={"删除 " + c.name}
                                disabled={!canEdit}
                                onClick={() => setDeleting(c)}
                              >
                                <Trash2 className="text-slate-400 hover:text-red-600" />
                              </Button>
                            </div>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            ) : (
              <Empty
                icon={page === "rtsp" ? Radio : FolderOpen}
                title={
                  query
                    ? "没有匹配的视频源"
                    : page === "rtsp"
                      ? "添加第一个 RTSP 源"
                      : "添加第一个文件源"
                }
                description={
                  query
                    ? "试试其他名称或地址。"
                    : page === "rtsp"
                      ? "填写摄像头的 RTSP 地址，即可统一调阅并在页面预览。"
                      : "指定服务器上的 MP4 或 MKV 文件路径，即可持续循环播放。"
                }
                action={
                  !query && (
                    <Button
                      variant="outline"
                      disabled={!canEdit}
                      onClick={() => setAdding(true)}
                    >
                      <Plus />
                      添加视频源
                    </Button>
                  )
                }
              />
            )}
          </section>
          <div className="footnote">
            <ShieldCheck size={14} />
            <span>
              配置文件：{data?.management.configFile || "—"} ·{" "}
              {data?.global.httpflv
                ? "HTTP-FLV 预览已启用"
                : "HTTP-FLV 未启用，预览暂不可用"}
            </span>
          </div>
        </main>
        <footer className="footer">
          <span>MediaMTX · 视频源控制台</span>
          <span>RTSP / HTTP-FLV</span>
        </footer>
      </div>
      <AddSource
        open={adding}
        kind={page === "file" ? "file" : "rtsp"}
        busy={busy}
        onClose={() => setAdding(false)}
        onSave={async (name, source) => {
          await save(
            {
              action: "add",
              name,
              path: {
                source,
                ...(page === "rtsp" ? { rtspTransport: "tcp" } : {}),
              },
            },
            "视频源已添加并保存到配置文件。",
          );
          setAdding(false);
        }}
      />
      <Dialog
        open={!!deleting}
        onOpenChange={(v) => {
          if (!v && !busy) setDeleting(null);
        }}
      >
        <DialogContent>
          <DialogTitle className="text-lg font-semibold">
            删除视频源
          </DialogTitle>
          <DialogDescription className="mt-3 text-sm leading-6">
            删除 <strong>{deleting?.name}</strong>{" "}
            的接入配置。正在调阅该源的客户端会断开；文件源的原始视频文件会保留。
          </DialogDescription>
          {error && (
            <p role="alert" className="mt-3 text-sm text-red-600">
              {error}
            </p>
          )}
          <div className="mt-6 flex justify-end gap-2">
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => setDeleting(null)}
            >
              取消
            </Button>
            <Button
              variant="destructive"
              disabled={busy}
              onClick={async () => {
                try {
                  await save(
                    { action: "delete", name: deleting?.name },
                    "视频源已删除。",
                  );
                  setDeleting(null);
                } catch {
                  /* error shown */
                }
              }}
            >
              {busy ? <Loader2 className="animate-spin" /> : <Trash2 />}确认删除
            </Button>
          </div>
        </DialogContent>
      </Dialog>
      <Connection
        open={connection}
        onClose={() => setConnection(false)}
        onConnect={async (value) => {
          setAuthorization(value);
          await refresh(true);
          setConnection(false);
        }}
      />
      {data && (
        <GBSettings
          open={gbSettings}
          config={data.global}
          busy={busy}
          canEdit={canEdit}
          onClose={() => setGBSettings(false)}
          onSave={async (global) => {
            await save(
              { action: "global", global },
              "国标服务配置已保存，服务已开始应用新配置。",
            );
            setGBSettings(false);
          }}
        />
      )}
      <Preview path={preview} onClose={() => setPreview(null)} />
    </div>
  );
}

function AddSource({
  open,
  kind,
  busy,
  onClose,
  onSave,
}: {
  open: boolean;
  kind: "rtsp" | "file";
  busy: boolean;
  onClose: () => void;
  onSave: (name: string, source: string) => Promise<void>;
}) {
  const [name, setName] = useState("");
  const [source, setSource] = useState("");
  const [error, setError] = useState("");
  useEffect(() => {
    if (open) {
      setName("");
      setSource("");
      setError("");
    }
  }, [open]);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    try {
      const n = name.trim();
      if (!n || n.startsWith("~") || n === "all" || n === "all_others")
        throw new Error("请输入一个具体的流路径名称。");
      let url = source.trim();
      if (kind === "file") {
        url = fileSource(url);
        if (!/\.(mp4|mkv)$/i.test(new URL(url).pathname))
          throw new Error("文件格式必须为 MP4 或 MKV。");
      } else {
        const parsed = new URL(url);
        if (!["rtsp:", "rtsps:"].includes(parsed.protocol) || !parsed.hostname)
          throw new Error("请输入有效的 RTSP 或 RTSPS 地址。");
      }
      await onSave(n, url);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };
  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v && !busy) onClose();
      }}
    >
      <DialogContent>
        <DialogTitle className="text-lg font-semibold">
          添加{kind === "file" ? "文件源" : " RTSP 源"}
        </DialogTitle>
        <DialogDescription className="mt-1 text-sm">
          保存后自动接入，并可通过 RTSP 或 HTTP-FLV 调阅。
        </DialogDescription>
        <form onSubmit={submit} className="mt-6 space-y-5">
          <Field
            label="流路径名称"
            hint="名称唯一，可使用 / 分组，例如 camera/entrance。"
          >
            <Input
              required
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={kind === "file" ? "files/demo" : "camera/entrance"}
              autoFocus
            />
          </Field>
          <Field
            label={kind === "file" ? "服务器文件路径" : "RTSP 源地址"}
            hint={
              kind === "file"
                ? "文件须已存在于 MediaMTX 服务器；这里不上传浏览器本机文件。支持 H.264/H.265，音频会被忽略。"
                : "支持 rtsp:// 和 rtsps://；源凭据仅用于服务端接入。"
            }
          >
            <Input
              required
              value={source}
              onChange={(e) => setSource(e.target.value)}
              placeholder={
                kind === "file"
                  ? "/data/videos/demo.mp4"
                  : "rtsp://user:password@192.168.1.10:554/stream"
              }
              className="font-mono text-xs"
            />
          </Field>
          {error && (
            <p role="alert" className="text-sm text-red-600">
              {error}
            </p>
          )}
          <div className="flex justify-end gap-2 border-t pt-5">
            <Button
              type="button"
              variant="outline"
              disabled={busy}
              onClick={onClose}
            >
              取消
            </Button>
            <Button type="submit" disabled={busy}>
              {busy ? <Loader2 className="animate-spin" /> : <Plus />}添加并保存
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
function Connection({
  open,
  onClose,
  onConnect,
}: {
  open: boolean;
  onClose: () => void;
  onConnect: (value: string) => Promise<void>;
}) {
  const [user, setUser] = useState("");
  const [password, setPassword] = useState("");
  const [token, setToken] = useState("");
  const [mode, setMode] = useState("basic");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v && !busy) onClose();
      }}
    >
      <DialogContent>
        <DialogTitle className="text-lg font-semibold">连接凭据</DialogTitle>
        <DialogDescription className="mt-2 text-sm">
          使用当前服务的 Control API
          凭据。凭据仅保存在本标签页会话中，预览还需要视频读取权限。
        </DialogDescription>
        <form
          className="mt-5 space-y-4"
          onSubmit={async (e) => {
            e.preventDefault();
            setBusy(true);
            setError("");
            try {
              let value = "";
              if (mode === "token" && token.trim())
                value = "Bearer " + token.trim();
              else if (mode === "basic" && user) {
                const bytes = new TextEncoder().encode(user + ":" + password);
                value =
                  "Basic " +
                  btoa(
                    Array.from(bytes, (b) => String.fromCharCode(b)).join(""),
                  );
              }
              await onConnect(value);
            } catch (e) {
              setError(String(e));
            } finally {
              setBusy(false);
            }
          }}
        >
          <Field label="鉴权方式">
            <select value={mode} onChange={(e) => setMode(e.target.value)}>
              <option value="basic">用户名 / 密码</option>
              <option value="token">Bearer Token</option>
              <option value="none">匿名访问</option>
            </select>
          </Field>
          {mode === "basic" && (
            <>
              <Field label="用户名">
                <Input
                  value={user}
                  onChange={(e) => setUser(e.target.value)}
                  autoComplete="username"
                />
              </Field>
              <Field label="密码">
                <Input
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  autoComplete="current-password"
                />
              </Field>
            </>
          )}
          {mode === "token" && (
            <Field label="Token">
              <Input
                type="password"
                value={token}
                onChange={(e) => setToken(e.target.value)}
              />
            </Field>
          )}
          {error && (
            <p role="alert" className="text-sm text-red-600">
              {error}
            </p>
          )}
          <div className="flex justify-end">
            <Button disabled={busy}>
              {busy ? <Loader2 className="animate-spin" /> : <Check />}连接服务
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
function GBSettings({
  open,
  config,
  busy,
  canEdit,
  onClose,
  onSave,
}: {
  open: boolean;
  config: GlobalConfig;
  busy: boolean;
  canEdit: boolean;
  onClose: () => void;
  onSave: (global: Record<string, unknown>) => Promise<void>;
}) {
  const [values, setValues] = useState<GlobalConfig>(config);
  const [password, setPassword] = useState("");
  const [clearPassword, setClearPassword] = useState(false);
  const [portMin, setPortMin] = useState("20000");
  const [portMax, setPortMax] = useState("20999");
  const [error, setError] = useState("");
  useEffect(() => {
    if (open) {
      setValues(config);
      setPassword("");
      setClearPassword(false);
      setPortMin(String(config.gb28181MediaPortRange[0]));
      setPortMax(String(config.gb28181MediaPortRange[1]));
      setError("");
    }
  }, [open]); // Refreshes must not overwrite an open form.
  const change = (key: string, value: unknown) =>
    setValues((prev) => ({ ...prev, [key]: value }));
  const fields: [string, string, string][] = [
    ["gb28181Serial", "平台编码", "20 位国标平台编码"],
    ["gb28181Realm", "SIP 域", "例如 3402000000"],
    ["gb28181Address", "SIP 监听地址", ":5060"],
    ["gb28181SIPIP", "对外 SIP IP", "留空自动检测"],
    ["gb28181MediaListenIP", "媒体监听 IP", "留空监听全部接口"],
    ["gb28181MediaIP", "对外媒体 IP", "留空使用 SIP IP"],
    ["gb28181KeepalivePeriod", "心跳超时", "120s"],
    ["gb28181PathTemplate", "通道流路径模板", "gb28181/$CHANNEL"],
  ];
  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v && !busy) onClose();
      }}
    >
      <DialogContent className="max-w-2xl">
        <DialogTitle className="text-lg font-semibold">
          国标服务配置
        </DialogTitle>
        <DialogDescription className="mt-1 text-sm">
          保存到配置文件并热加载；修改服务参数可能中断现有国标连接。
        </DialogDescription>
        <form
          className="mt-5 space-y-5"
          onSubmit={async (e) => {
            e.preventDefault();
            setError("");
            try {
              if (!/^\d{20}$/.test(values.gb28181Serial))
                throw new Error("平台编码必须为 20 位数字。");
              const min = Number(portMin),
                max = Number(portMax);
              if (
                !Number.isInteger(min) ||
                !Number.isInteger(max) ||
                min < 1 ||
                max > 65535 ||
                min > max
              )
                throw new Error("请输入有效媒体端口范围（1–65535）。");
              if (!values.gb28181PathTemplate.includes("$CHANNEL"))
                throw new Error("路径模板须包含 $CHANNEL。");
              if (!values.gb28181Transports.length)
                throw new Error("至少选择一种 SIP 传输协议。");
              const patch: Record<string, unknown> = {
                gb28181: values.gb28181,
                gb28181MediaProtocol: values.gb28181MediaProtocol,
                gb28181Transports: values.gb28181Transports,
                gb28181MediaPortRange: [min, max],
              };
              for (const [key] of fields) patch[key] = values[key];
              if (clearPassword) patch.gb28181Password = "";
              else if (password) patch.gb28181Password = password;
              await onSave(patch);
            } catch (e) {
              setError(e instanceof Error ? e.message : String(e));
            }
          }}
        >
          <label className="flex items-center justify-between rounded-xl border bg-slate-50 p-4">
            <span className="text-sm font-medium">启用 GB28181 上级平台</span>
            <input
              type="checkbox"
              checked={values.gb28181}
              onChange={(e) => change("gb28181", e.target.checked)}
              className="size-5 accent-blue-600"
            />
          </label>
          <div className="grid gap-4 sm:grid-cols-2">
            {fields.map(([key, label, placeholder]) => (
              <Field key={key} label={label}>
                <Input
                  value={String(values[key] || "")}
                  onChange={(e) => change(key, e.target.value)}
                  placeholder={placeholder}
                  className="font-mono text-xs"
                />
              </Field>
            ))}
            <Field label="媒体传输协议">
              <select
                value={values.gb28181MediaProtocol}
                onChange={(e) => change("gb28181MediaProtocol", e.target.value)}
              >
                <option value="udp">UDP</option>
                <option value="tcp">TCP</option>
              </select>
            </Field>
            <Field label="媒体端口范围">
              <div className="flex items-center gap-2">
                <Input
                  type="number"
                  min={1}
                  max={65535}
                  required
                  value={portMin}
                  onChange={(e) => setPortMin(e.target.value)}
                  aria-label="起始端口"
                />
                <span>—</span>
                <Input
                  type="number"
                  min={1}
                  max={65535}
                  required
                  value={portMax}
                  onChange={(e) => setPortMax(e.target.value)}
                  aria-label="结束端口"
                />
              </div>
            </Field>
            <Field label="SIP 传输协议">
              <div className="flex h-10 items-center gap-5">
                {["udp", "tcp"].map((tr) => (
                  <label key={tr} className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      checked={values.gb28181Transports.includes(tr)}
                      onChange={(e) =>
                        change(
                          "gb28181Transports",
                          e.target.checked
                            ? [...values.gb28181Transports, tr]
                            : values.gb28181Transports.filter((t) => t !== tr),
                        )
                      }
                    />
                    {tr.toUpperCase()}
                  </label>
                ))}
              </div>
            </Field>
            <Field
              label="注册密码"
              hint="留空保留现有密码；现有密码不会返回浏览器。"
            >
              <Input
                type="password"
                value={password}
                disabled={clearPassword}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="new-password"
                placeholder="输入新密码"
              />
            </Field>
          </div>
          <label className="flex items-center gap-2 text-sm text-muted-foreground">
            <input
              type="checkbox"
              checked={clearPassword}
              onChange={(e) => setClearPassword(e.target.checked)}
            />
            清空注册密码，允许无密码注册
          </label>
          {error && (
            <p role="alert" className="text-sm text-red-600">
              {error}
            </p>
          )}
          <div className="flex justify-end gap-2 border-t pt-4">
            <Button
              type="button"
              variant="outline"
              disabled={busy}
              onClick={onClose}
            >
              取消
            </Button>
            <Button disabled={!canEdit}>
              {busy ? <Loader2 className="animate-spin" /> : <Check />}保存配置
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

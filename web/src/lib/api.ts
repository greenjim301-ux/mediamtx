export type PathConf = {
  name: string;
  source: string;
  sourceOnDemand: boolean;
  rtspTransport: string;
};
export type RuntimePath = {
  name: string;
  online: boolean;
  available: boolean;
  tracks?: string[];
  readers?: unknown[];
  inboundBytes: number;
  outboundBytes: number;
};
export type Channel = {
  pathConfigured: boolean;
  pathConflict: boolean;
  pathConfigName?: string;
  id: string;
  deviceId: string;
  name: string;
  manufacturer: string;
  model: string;
  address: string;
  parentId: string;
  online: boolean;
  path: string;
};
export type Device = {
  id: string;
  name: string;
  deviceType: string;
  address: string;
  transport: string;
  online: boolean;
  channels: Channel[];
  lastKeepaliveTime?: string;
};
export type GlobalConfig = {
  gb28181: boolean;
  gb28181Address: string;
  gb28181Transports: string[];
  gb28181Serial: string;
  gb28181Realm: string;
  gb28181SIPIP: string;
  gb28181MediaListenIP: string;
  gb28181MediaIP: string;
  gb28181MediaProtocol: string;
  gb28181MediaPortRange: number[];
  gb28181KeepalivePeriod: string;
  gb28181PathTemplate: string;
  httpflv: boolean;
  httpflvAddress: string;
  rtspAddress: string;
  [key: string]: unknown;
};
export type Management = { configFile: string; persistent: boolean };
export type Snapshot = {
  configs: PathConf[];
  paths: RuntimePath[];
  devices: Device[];
  channels: Channel[];
  global: GlobalConfig;
  management: Management;
};
let authorization =
  typeof sessionStorage === "undefined"
    ? ""
    : sessionStorage.getItem("mediamtx.authorization") || "";
export function setAuthorization(value: string) {
  authorization = value;
  if (value) sessionStorage.setItem("mediamtx.authorization", value);
  else sessionStorage.removeItem("mediamtx.authorization");
}
export function authHeaders(): Record<string, string> {
  return authorization ? { Authorization: authorization } : {};
}
export async function request<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const response = await fetch("/v3" + path, {
    ...options,
    headers: {
      ...authHeaders(),
      ...(options.body ? { "Content-Type": "application/json" } : {}),
      ...options.headers,
    },
    signal: options.signal || AbortSignal.timeout(15000),
    cache: "no-store",
  });
  if (!response.ok) {
    let message = `请求失败 (${response.status})`;
    try {
      const data = await response.json();
      message = data.error || message;
    } catch {
      /* non-JSON response */
    }
    throw new Error(
      response.status === 401
        ? "需要 Control API 权限，请检查连接凭据。"
        : message,
    );
  }
  return (await response.json()) as T;
}
export async function list<T>(path: string) {
  const out: T[] = [];
  let page = 0,
    pageCount = 1;
  do {
    const data = await request<{ items: T[]; pageCount: number }>(
      `${path}?itemsPerPage=100&page=${page}`,
    );
    out.push(...data.items);
    pageCount = data.pageCount;
    page++;
  } while (page < pageCount);
  return out;
}
export async function snapshot(): Promise<Snapshot> {
  const [global, management, configs, paths] = await Promise.all([
    request<GlobalConfig>("/config/global/get"),
    request<Management>("/management/info"),
    list<PathConf>("/config/paths/list"),
    list<RuntimePath>("/paths/list"),
  ]);
  let devices: Device[] = [],
    channels: Channel[] = [];
  if (global.gb28181) {
    [devices, channels] = await Promise.all([
      list<Device>("/gb28181/devices/list"),
      list<Channel>("/gb28181/channels/list"),
    ]);
  }
  return { global, management, configs, paths, devices, channels };
}
export async function apply(change: unknown) {
  return request("/management/apply", {
    method: "POST",
    body: JSON.stringify(change),
  });
}
export function previewURL(name: string) {
  return (
    "/v3/preview/" + name.split("/").map(encodeURIComponent).join("/") + ".flv"
  );
}
export function fileSource(path: string) {
  if (path.startsWith("file://")) return path;
  if (!path.startsWith("/"))
    throw new Error("请输入服务器上的绝对路径，例如 /data/video.mp4");
  return "file://" + path.split("/").map(encodeURIComponent).join("/");
}
export function hideCredentials(source: string) {
  return source.replace(/(rtsps?:\/\/)[^/@]+@/i, "$1••••@");
}

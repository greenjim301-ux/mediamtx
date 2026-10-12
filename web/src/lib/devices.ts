import type { Device, Channel } from "./api";
export type Kind = "camera" | "platform" | "recorder" | "directory" | "unknown";
const types: Record<string, Kind> = {
  "131": "camera",
  "132": "camera",
  "111": "recorder",
  "118": "recorder",
  "200": "platform",
  "215": "directory",
  "216": "directory",
};
export function classify(
  id: string,
  deviceType = "",
): { kind: Kind; evidence: string } {
  if (/^\d{20}$/.test(id)) {
    const code = id.slice(10, 13);
    if (types[code])
      return { kind: types[code], evidence: `国标编码第 11–13 位：${code}` };
  }
  if (/^(ipc|camera|ipcamera|摄像机|网络摄像机)$/i.test(deviceType.trim()))
    return { kind: "camera", evidence: `设备上报类型：${deviceType}` };
  return { kind: "unknown", evidence: "编码或上报类型无法确认，需人工核对" };
}
export function classifyChannel(c: Channel, devices: Device[]) {
  const d = devices.find((d) => d.id === c.deviceId);
  return classify(c.id, c.id === d?.id ? d.deviceType : "");
}
export const kindLabels: Record<Kind, string> = {
  camera: "摄像头",
  platform: "下级平台",
  recorder: "录像设备",
  directory: "目录节点",
  unknown: "待识别",
};

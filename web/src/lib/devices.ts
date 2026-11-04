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

// Binding state comes from Go's actual path matcher, including regex precedence.
export function channelPreviewAction(
  channel: Pick<Channel, "pathConfigured" | "pathConflict">,
): "preview" | "add" | "conflict" {
  if (channel.pathConflict) return "conflict";
  return channel.pathConfigured ? "preview" : "add";
}

export type GBPTZInfo = {
  supported: boolean;
  label: string;
  reason: string;
};

export function checkGBPTZ(c: Channel): GBPTZInfo {
  // 1. Positive identification: hardware model, GB 20-digit code, or explicit PTZType
  // 1a. Device/Channel Model or Name contains PTZ keywords (most direct physical evidence)
  const text = ((c.name || "") + " " + (c.model || "")).toLowerCase();
  const keywordMatch = text.match(/(ptz|球机|云台|高速球|智能球|转台)/i);
  if (keywordMatch) {
    return {
      supported: true,
      label: "球机 (PTZ)",
      reason: `设备型号/名称包含云台关键字 ("${keywordMatch[0]}")`,
    };
  }

  // 1b. 20-digit GB code 11~13th digits: 132 = 遥控/云台摄像机; 133/134/135 = 遥控球机/云台
  if (/^\d{20}$/.test(c.id)) {
    const code = c.id.slice(10, 13);
    if (code === "132" || code === "133" || code === "134" || code === "135") {
      return {
        supported: true,
        label: "球机 (PTZ)",
        reason: `国标编码 11~13 位为 ${code} (遥控/云台摄像机)`,
      };
    }
  }

  // 1c. Catalog XML PTZType explicitly declares PTZ (1: 球机, 2: 半球, 4: 遥控枪机)
  if (c.ptzType === 1) {
    return { supported: true, label: "球机 (PTZ)", reason: "目录上报 PTZType=1 (球机)" };
  }
  if (c.ptzType === 2) {
    return { supported: true, label: "半球 (PTZ)", reason: "目录上报 PTZType=2 (半球)" };
  }
  if (c.ptzType === 4) {
    return { supported: true, label: "遥控枪机", reason: "目录上报 PTZType=4 (遥控枪机)" };
  }

  // 2. Negative indicators (only when none of the positive indicators matched)
  if (c.ptzType === 3) {
    return { supported: false, label: "固定枪机", reason: "目录上报 PTZType=3 (固定枪机)" };
  }
  if (/^\d{20}$/.test(c.id) && c.id.slice(10, 13) === "131") {
    return { supported: false, label: "枪机", reason: "国标编码 131 (固定摄像机)" };
  }

  return { supported: false, label: "普通摄像机", reason: "未检测到云台属性" };
}

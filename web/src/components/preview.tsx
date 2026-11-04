import { useEffect, useRef, useState, useCallback } from "react";
import {
  AlertCircle,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronUp,
  Loader2,
  RotateCw,
  Square,
  ZoomIn,
  ZoomOut,
} from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
} from "./ui/dialog";
import { Button } from "./ui/button";
import { authHeaders, onvifPTZ, previewURL } from "@/lib/api";

interface Player {
  attachMediaElement(v: HTMLVideoElement): void;
  detachMediaElement(): void;
  load(): void;
  unload(): void;
  play(): Promise<void> | undefined;
  destroy(): void;
  on(event: string, handler: (...args: unknown[]) => void): void;
}

declare global {
  interface Window {
    mpegts?: {
      isSupported(): boolean;
      getFeatureList(): { mseH265Playback?: boolean };
      createPlayer(source: unknown, config: unknown): Player;
      Events: { ERROR: string };
    };
  }
}

export function Preview({
  path,
  hasPTZ = false,
  onClose,
}: {
  path: string | null;
  hasPTZ?: boolean;
  onClose: () => void;
}) {
  const video = useRef<HTMLVideoElement>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [attempt, setAttempt] = useState(0);
  const [autoplayBlocked, setAutoplayBlocked] = useState(false);

  // PTZ states
  const [speed, setSpeed] = useState(0.5);
  const [ptzStatus, setPtzStatus] = useState<string>("");
  const [ptzError, setPtzError] = useState<string>("");
  const activeCmdRef = useRef<string | null>(null);

  const sendPTZ = useCallback(
    async (
      cmd: "up" | "down" | "left" | "right" | "zoomin" | "zoomout" | "stop",
    ) => {
      if (!path) return;
      setPtzError("");
      try {
        if (cmd === "stop") {
          activeCmdRef.current = null;
          setPtzStatus("");
          await onvifPTZ(path, { command: "stop" });
        } else {
          activeCmdRef.current = cmd;
          const labels: Record<string, string> = {
            up: "上仰",
            down: "俯倾",
            left: "左转",
            right: "右转",
            zoomin: "变焦放大",
            zoomout: "变焦缩小",
          };
          setPtzStatus(labels[cmd] || cmd);
          await onvifPTZ(path, { command: cmd, speed });
        }
      } catch (e) {
        setPtzError(e instanceof Error ? e.message : String(e));
      }
    },
    [path, speed],
  );

  const handleMouseDown = (
    cmd: "up" | "down" | "left" | "right" | "zoomin" | "zoomout",
  ) => {
    sendPTZ(cmd);
  };

  const handleMouseUp = () => {
    if (activeCmdRef.current) {
      sendPTZ("stop");
    }
  };

  useEffect(() => {
    if (!path) return;
    setError("");
    setLoading(true);
    setAutoplayBlocked(false);
    setPtzStatus("");
    setPtzError("");
    activeCmdRef.current = null;

    const lib = window.mpegts;
    let player: Player | undefined;
    let disposed = false;
    const timer = setTimeout(() => {
      if (!disposed)
        setError(
          "尚未收到可播放的视频。请确认源在线；新连接可能需要等待关键帧。",
        );
    }, 20000);
    timerRef.current = timer;

    // Radix mounts the video after the dialog opens.
    const begin = setTimeout(() => {
      if (!lib?.isSupported() || !video.current) {
        clearTimeout(timer);
        setError("当前浏览器不支持 MSE 实时播放，请使用支持的视频浏览器。");
        setLoading(false);
        return;
      }
      try {
        player = lib.createPlayer(
          { type: "flv", url: previewURL(path), isLive: true },
          {
            enableWorker: false,
            enableStashBuffer: false,
            liveBufferLatencyChasing: true,
            headers: authHeaders(),
          },
        );
        player.on(lib.Events.ERROR, (...args) => {
          if (!disposed) {
            clearTimeout(timer);
            setError("预览失败：" + args.map(String).join(" · "));
            setLoading(false);
          }
        });
        player.attachMediaElement(video.current);
        player.load();
        player.play()?.catch(() => {
          if (!disposed) setAutoplayBlocked(true);
        });
      } catch (e) {
        clearTimeout(timer);
        setError(String(e));
        setLoading(false);
      }
    }, 0);

    return () => {
      disposed = true;
      clearTimeout(begin);
      clearTimeout(timer);
      if (player) {
        player.unload();
        player.detachMediaElement();
        player.destroy();
      }
    };
  }, [path, attempt]);

  return (
    <Dialog
      open={!!path}
      onOpenChange={(open) => {
        if (!open) {
          if (activeCmdRef.current) {
            sendPTZ("stop");
          }
          onClose();
        }
      }}
    >
      <DialogContent className={hasPTZ ? "max-w-5xl" : "max-w-4xl"}>
        <div className="flex items-center justify-between pr-6">
          <div>
            <DialogTitle className="text-lg font-semibold">
              实时预览 {hasPTZ && <span className="text-sm font-normal text-blue-600">· 支持 ONVIF PTZ</span>}
            </DialogTitle>
            <DialogDescription className="mt-1 font-mono text-xs">
              {path} · HTTP-FLV
            </DialogDescription>
          </div>
        </div>

        <div className={`mt-4 grid gap-5 ${hasPTZ ? "lg:grid-cols-3" : "grid-cols-1"}`}>
          {/* Video Player */}
          <div className={hasPTZ ? "lg:col-span-2" : "col-span-1"}>
            <div className="relative overflow-hidden rounded-xl bg-slate-950">
              <video
                ref={video}
                controls
                muted
                autoPlay
                playsInline
                className="aspect-video w-full"
                onPlaying={() => {
                  clearTimeout(timerRef.current);
                  setLoading(false);
                  setError("");
                  setAutoplayBlocked(false);
                }}
              />
              {loading && !error && (
                <div className="pointer-events-none absolute inset-0 flex items-center justify-center gap-2 text-sm text-slate-300">
                  <Loader2 className="animate-spin" size={18} />
                  连接视频源，等待关键帧…
                </div>
              )}
            </div>

            {autoplayBlocked && (
              <p className="mt-3 text-sm text-muted-foreground">
                浏览器阻止了自动播放，请点击播放器的播放按钮。
              </p>
            )}

            {error && (
              <div
                role="alert"
                className="mt-3 flex items-start gap-2 rounded-lg bg-red-50 p-3 text-sm text-red-700"
              >
                <AlertCircle size={17} className="mt-0.5 shrink-0" />
                {error}
              </div>
            )}
          </div>

          {/* PTZ Controller (if supported) */}
          {hasPTZ && (
            <div className="flex flex-col justify-between rounded-xl border border-slate-200 bg-slate-50/70 p-4">
              <div>
                <div className="flex items-center justify-between border-b pb-2">
                  <span className="text-sm font-semibold text-slate-700">云台控制 (PTZ)</span>
                  {ptzStatus ? (
                    <span className="inline-flex items-center gap-1 rounded bg-blue-100 px-2 py-0.5 text-xs font-medium text-blue-700 animate-pulse">
                      正在{ptzStatus}
                    </span>
                  ) : (
                    <span className="text-xs text-slate-400">长按方向移动</span>
                  )}
                </div>

                {/* Direction Pad */}
                <div className="mt-5 flex flex-col items-center">
                  <div className="grid grid-cols-3 gap-2">
                    <div />
                    <Button
                      type="button"
                      variant="outline"
                      size="icon"
                      className="size-11 select-none active:bg-blue-100"
                      title="上仰"
                      onMouseDown={() => handleMouseDown("up")}
                      onMouseUp={handleMouseUp}
                      onMouseLeave={handleMouseUp}
                      onTouchStart={() => handleMouseDown("up")}
                      onTouchEnd={handleMouseUp}
                    >
                      <ChevronUp size={22} />
                    </Button>
                    <div />

                    <Button
                      type="button"
                      variant="outline"
                      size="icon"
                      className="size-11 select-none active:bg-blue-100"
                      title="左转"
                      onMouseDown={() => handleMouseDown("left")}
                      onMouseUp={handleMouseUp}
                      onMouseLeave={handleMouseUp}
                      onTouchStart={() => handleMouseDown("left")}
                      onTouchEnd={handleMouseUp}
                    >
                      <ChevronLeft size={22} />
                    </Button>

                    <Button
                      type="button"
                      variant="outline"
                      size="icon"
                      className="size-11 select-none text-slate-600 hover:text-red-600 active:bg-red-50"
                      title="停止"
                      onClick={() => sendPTZ("stop")}
                    >
                      <Square size={16} />
                    </Button>

                    <Button
                      type="button"
                      variant="outline"
                      size="icon"
                      className="size-11 select-none active:bg-blue-100"
                      title="右转"
                      onMouseDown={() => handleMouseDown("right")}
                      onMouseUp={handleMouseUp}
                      onMouseLeave={handleMouseUp}
                      onTouchStart={() => handleMouseDown("right")}
                      onTouchEnd={handleMouseUp}
                    >
                      <ChevronRight size={22} />
                    </Button>

                    <div />
                    <Button
                      type="button"
                      variant="outline"
                      size="icon"
                      className="size-11 select-none active:bg-blue-100"
                      title="俯倾"
                      onMouseDown={() => handleMouseDown("down")}
                      onMouseUp={handleMouseUp}
                      onMouseLeave={handleMouseUp}
                      onTouchStart={() => handleMouseDown("down")}
                      onTouchEnd={handleMouseUp}
                    >
                      <ChevronDown size={22} />
                    </Button>
                    <div />
                  </div>
                </div>

                {/* Zoom Controls */}
                <div className="mt-5 border-t pt-4">
                  <span className="block text-xs font-medium text-slate-600">镜头变焦 (Zoom)</span>
                  <div className="mt-2 grid grid-cols-2 gap-2">
                    <Button
                      type="button"
                      variant="outline"
                      className="h-9 gap-1 text-xs select-none active:bg-blue-100"
                      onMouseDown={() => handleMouseDown("zoomin")}
                      onMouseUp={handleMouseUp}
                      onMouseLeave={handleMouseUp}
                      onTouchStart={() => handleMouseDown("zoomin")}
                      onTouchEnd={handleMouseUp}
                    >
                      <ZoomIn size={15} />
                      放大 (+)
                    </Button>

                    <Button
                      type="button"
                      variant="outline"
                      className="h-9 gap-1 text-xs select-none active:bg-blue-100"
                      onMouseDown={() => handleMouseDown("zoomout")}
                      onMouseUp={handleMouseUp}
                      onMouseLeave={handleMouseUp}
                      onTouchStart={() => handleMouseDown("zoomout")}
                      onTouchEnd={handleMouseUp}
                    >
                      <ZoomOut size={15} />
                      缩小 (-)
                    </Button>
                  </div>
                </div>

                {/* Speed Slider */}
                <div className="mt-4 border-t pt-3">
                  <div className="flex items-center justify-between text-xs text-slate-600">
                    <span>云台速度</span>
                    <span className="font-mono font-semibold">{Math.round(speed * 100)}%</span>
                  </div>
                  <input
                    type="range"
                    min={0.1}
                    max={1.0}
                    step={0.1}
                    value={speed}
                    onChange={(e) => setSpeed(parseFloat(e.target.value))}
                    className="mt-2 h-1.5 w-full cursor-pointer accent-blue-600"
                  />
                  <div className="mt-1 flex justify-between text-[10px] text-slate-400">
                    <span>微动 (10%)</span>
                    <span>快速 (100%)</span>
                  </div>
                </div>

                {ptzError && (
                  <p className="mt-2 text-xs text-red-600">
                    PTZ 指令异常: {ptzError}
                  </p>
                )}
              </div>

              <div className="mt-3 text-center text-[11px] text-slate-400">
                支持鼠标按住连续转动，松开即停
              </div>
            </div>
          )}
        </div>

        <div className="mt-4 flex items-center justify-between gap-3 border-t pt-3">
          <p className="text-xs text-muted-foreground">
            H.265 需浏览器支持 HEVC；ONVIF 流支持 HTTP-FLV 秒级低延迟预览。
          </p>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setAttempt((n) => n + 1)}
          >
            <RotateCw size={14} />
            重新连接
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

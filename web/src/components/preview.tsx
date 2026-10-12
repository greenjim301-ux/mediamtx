import { useEffect, useRef, useState } from "react";
import { AlertCircle, Loader2, RotateCw } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
} from "./ui/dialog";
import { Button } from "./ui/button";
import { authHeaders, previewURL } from "@/lib/api";
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
  onClose,
}: {
  path: string | null;
  onClose: () => void;
}) {
  const video = useRef<HTMLVideoElement>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [attempt, setAttempt] = useState(0);
  const [autoplayBlocked, setAutoplayBlocked] = useState(false);
  useEffect(() => {
    if (!path) return;
    setError("");
    setLoading(true);
    setAutoplayBlocked(false);
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
        if (!open) onClose();
      }}
    >
      <DialogContent className="max-w-4xl">
        <DialogTitle className="text-lg font-semibold">实时预览</DialogTitle>
        <DialogDescription className="mt-1 font-mono text-xs">
          {path} · HTTP-FLV
        </DialogDescription>
        <div className="relative mt-5 overflow-hidden rounded-xl bg-slate-950">
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
        <div className="mt-4 flex items-center justify-between gap-3">
          <p className="text-xs text-muted-foreground">
            H.265 需浏览器支持 HEVC；文件源仅提供视频，共享循环进度。
          </p>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setAttempt((n) => n + 1)}
          >
            <RotateCw />
            重新连接
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

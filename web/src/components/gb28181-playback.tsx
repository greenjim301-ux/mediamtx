import { useCallback, useEffect, useRef, useState } from "react";
import { AlertCircle, Calendar, History, Loader2, Pause, Play, RotateCw, Search } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "./ui/dialog";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import {
  authHeaders,
  gb28181ChannelRecords,
  previewURL,
  type Channel,
  type GB28181RecordItem,
} from "@/lib/api";

interface Player {
  attachMediaElement(v: HTMLVideoElement): void;
  detachMediaElement(): void;
  load(): void;
  unload(): void;
  play(): Promise<void> | undefined;
  destroy(): void;
  on(event: string, handler: (...args: unknown[]) => void): void;
}

function todayDateString(): string {
  const now = new Date();
  const y = now.getFullYear();
  const m = String(now.getMonth() + 1).padStart(2, "0");
  const d = String(now.getDate()).padStart(2, "0");
  return `${y}-${m}-${d}`;
}

function parseRecordTimeUnix(raw: string): number {
  const ms = Date.parse(raw);
  return Number.isNaN(ms) ? 0 : Math.floor(ms / 1000);
}

function formatDuration(startUnix: number, endUnix: number): string {
  const sec = Math.max(0, endUnix - startUnix);
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = sec % 60;
  return [h, m, s].map((v) => String(v).padStart(2, "0")).join(":");
}

function shortTime(raw: string): string {
  const idx = raw.indexOf("T");
  return idx >= 0 ? raw.slice(idx + 1, idx + 9) : raw;
}

function formatUnixClock(unixSec: number): string {
  const d = new Date(unixSec * 1000);
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  const ss = String(d.getSeconds()).padStart(2, "0");
  return `${hh}:${mm}:${ss}`;
}

export function GB28181PlaybackDialog({
  channel,
  onClose,
}: {
  channel: Channel | null;
  onClose: () => void;
}) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const trackRef = useRef<HTMLDivElement>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const pauseTimerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const displayOffsetRef = useRef(0);
  const pausedOffsetRef = useRef(0);

  const [date, setDate] = useState(todayDateString);
  const [recordType, setRecordType] = useState("all");
  const [querying, setQuerying] = useState(false);
  const [queryError, setQueryError] = useState("");
  const [records, setRecords] = useState<GB28181RecordItem[]>([]);

  const [selected, setSelected] = useState<GB28181RecordItem | null>(null);
  const [seekOffset, setSeekOffset] = useState(0);
  const [draggingOffset, setDraggingOffset] = useState<number | null>(null);
  const [hoverInfo, setHoverInfo] = useState<{ offset: number; percent: number } | null>(null);
  const [currentTimeSec, setCurrentTimeSec] = useState(0);
  const [paused, setPaused] = useState(false);
  const [suspended, setSuspended] = useState(false);
  const [loading, setLoading] = useState(false);
  const [ended, setEnded] = useState(false);
  const [playError, setPlayError] = useState("");
  const [autoplayBlocked, setAutoplayBlocked] = useState(false);
  const [attempt, setAttempt] = useState(0);

  const queryRecords = useCallback(async () => {
    if (!channel) return;
    setQuerying(true);
    setQueryError("");
    try {
      const start = `${date}T00:00:00`;
      const end = `${date}T23:59:59`;
      const res = await gb28181ChannelRecords(channel.id, start, end, recordType);
      setRecords(res.items || []);
    } catch (e) {
      setQueryError(e instanceof Error ? e.message : String(e));
      setRecords([]);
    } finally {
      setQuerying(false);
    }
  }, [channel, date, recordType]);

  useEffect(() => {
    clearTimeout(pauseTimerRef.current);
    if (!channel) {
      setRecords([]);
      setSelected(null);
      setPaused(false);
      setSuspended(false);
      setQueryError("");
      setPlayError("");
      setEnded(false);
      return;
    }
    setDate(todayDateString());
    setSelected(null);
    setSeekOffset(0);
    setDraggingOffset(null);
    setCurrentTimeSec(0);
    setPaused(false);
    setSuspended(false);
    setPlayError("");
    setEnded(false);
  }, [channel]);

  useEffect(() => {
    if (channel) {
      void queryRecords();
    }
  }, [channel, queryRecords]);

  const segStart = selected ? parseRecordTimeUnix(selected.startTime) : 0;
  const segEnd = selected ? parseRecordTimeUnix(selected.endTime) : 0;
  const totalSec = segStart > 0 && segEnd > segStart ? segEnd - segStart : 0;
  const playStartUnix = segStart > 0 ? Math.min(segEnd - 1, segStart + seekOffset) : 0;
  const displayOffset = Math.min(totalSec, seekOffset + Math.floor(currentTimeSec));
  displayOffsetRef.current = displayOffset;

  const playbackPath = (() => {
    if (!channel || !selected || suspended || segStart <= 0 || segEnd <= playStartUnix) return null;
    return `gb28181_playback/${channel.id}/${playStartUnix}/${segEnd}`;
  })();

  const commitSeek = useCallback(
    (targetOffset: number) => {
      clearTimeout(pauseTimerRef.current);
      const clamped = Math.max(0, Math.min(Math.max(0, totalSec - 1), targetOffset));
      setDraggingOffset(null);
      setPaused(false);
      setSuspended(false);
      setSeekOffset(clamped);
      setCurrentTimeSec(0);
      setEnded(false);
      setPlayError("");
      setAttempt((a) => a + 1);
    },
    [totalSec],
  );

  const handleVideoPaused = useCallback(() => {
    if (!selected || ended || suspended || playError) return;
    setPaused(true);
    clearTimeout(pauseTimerRef.current);
    pauseTimerRef.current = setTimeout(() => {
      pausedOffsetRef.current = displayOffsetRef.current;
      setSuspended(true);
    }, 30000);
  }, [selected, ended, suspended, playError]);

  const handleVideoResumed = useCallback(() => {
    clearTimeout(pauseTimerRef.current);
    setPaused(false);
  }, []);

  const togglePause = useCallback(() => {
    if (!selected) return;
    if (suspended) {
      commitSeek(pausedOffsetRef.current);
      return;
    }
    const v = videoRef.current;
    if (!v) return;
    if (v.paused) {
      void v.play();
    } else {
      v.pause();
    }
  }, [selected, suspended, commitSeek]);

  useEffect(() => {
    if (!playbackPath) {
      setLoading(false);
      return;
    }
    setLoading(true);
    setEnded(false);
    setPlayError("");
    setAutoplayBlocked(false);

    const lib = window.mpegts;
    let player: Player | undefined;
    let disposed = false;

    const timer = setTimeout(() => {
      if (!disposed) {
        setPlayError("尚未收到录像流，请确认 NVR 通道在线且该时间段存在录像。");
      }
    }, 20000);
    timerRef.current = timer;

    const begin = setTimeout(() => {
      if (!lib?.isSupported() || !videoRef.current) {
        clearTimeout(timer);
        setPlayError("当前浏览器不支持 MSE 播放。");
        setLoading(false);
        return;
      }
      try {
        player = lib.createPlayer(
          { type: "flv", url: previewURL(playbackPath), isLive: false },
          {
            enableWorker: false,
            enableStashBuffer: true,
            headers: authHeaders(),
          },
        );
        player.on(lib.Events.ERROR, (...args) => {
          if (!disposed) {
            clearTimeout(timer);
            setPlayError("回放失败：" + args.map(String).join(" · "));
            setLoading(false);
          }
        });
        player.attachMediaElement(videoRef.current);
        player.load();
        player.play()?.catch(() => {
          if (!disposed) setAutoplayBlocked(true);
        });
      } catch (e) {
        clearTimeout(timer);
        setPlayError(String(e));
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
  }, [playbackPath, attempt]);

  const offsetFromClientX = useCallback(
    (clientX: number) => {
      const el = trackRef.current;
      if (!el || totalSec <= 0) return { offset: 0, percent: 0 };
      const rect = el.getBoundingClientRect();
      if (rect.width <= 0) return { offset: 0, percent: 0 };
      const ratio = Math.max(0, Math.min(1, (clientX - rect.left) / rect.width));
      return {
        offset: Math.round(ratio * totalSec),
        percent: ratio * 100,
      };
    },
    [totalSec],
  );

  const activeOffset = draggingOffset ?? displayOffset;
  const activePercent = totalSec > 0 ? Math.min(100, (activeOffset / totalSec) * 100) : 0;

  return (
    <Dialog
      open={!!channel}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent className="max-w-6xl">
        <div className="pr-6">
          <DialogTitle className="flex items-center gap-2 text-lg font-semibold">
            <History className="size-5 text-blue-600" />
            <span>国标录像回放 · {channel?.name || channel?.id}</span>
          </DialogTitle>
          <DialogDescription className="mt-1 font-mono text-xs">
            通道 {channel?.id} · {playbackPath || "请选择录像片段开始回放"}
          </DialogDescription>
        </div>

        {/* 查询栏 */}
        <div className="mt-3 flex flex-wrap items-center gap-2 rounded-lg border bg-slate-50/70 p-2.5">
          <Calendar className="size-4 text-slate-500" />
          <Input
            type="date"
            value={date}
            onChange={(e) => setDate(e.target.value)}
            className="h-8 w-40 bg-white text-xs"
          />
          <select
            value={recordType}
            onChange={(e) => setRecordType(e.target.value)}
            className="h-8 rounded-md border border-input bg-white px-2 text-xs"
          >
            <option value="all">全部类型</option>
            <option value="time">定时录像</option>
            <option value="alarm">报警录像</option>
            <option value="manual">手动录像</option>
          </select>
          <Button
            size="sm"
            variant="outline"
            disabled={querying}
            onClick={() => void queryRecords()}
          >
            {querying ? <Loader2 className="animate-spin" /> : <Search />}
            查询录像
          </Button>
          <span className="ml-auto text-xs text-slate-500">
            共 {records.length} 条记录
          </span>
        </div>

        <div className="mt-3 grid gap-4 lg:grid-cols-3">
          {/* 录像片段列表 */}
          <div className="flex max-h-[420px] flex-col overflow-hidden rounded-xl border bg-white">
            <div className="border-b bg-slate-50 px-3 py-2 text-xs font-semibold text-slate-700">
              录像片段
            </div>
            <div className="flex-1 overflow-y-auto divide-y">
              {queryError ? (
                <div className="p-4 text-xs text-red-600">{queryError}</div>
              ) : querying && records.length === 0 ? (
                <div className="flex items-center gap-2 p-4 text-xs text-slate-500">
                  <Loader2 className="size-4 animate-spin" />
                  正在向 NVR 查询录像...
                </div>
              ) : records.length === 0 ? (
                <div className="p-4 text-xs text-slate-500">
                  该日期暂无录像记录。
                </div>
              ) : (
                records.map((item, idx) => {
                  const sUnix = parseRecordTimeUnix(item.startTime);
                  const eUnix = parseRecordTimeUnix(item.endTime);
                  const isActive =
                    selected?.startTime === item.startTime &&
                    selected?.endTime === item.endTime &&
                    selected?.filePath === item.filePath;
                  return (
                    <button
                      key={`${item.filePath}-${idx}`}
                      type="button"
                      onClick={() => {
                        clearTimeout(pauseTimerRef.current);
                        setSelected(item);
                        setSeekOffset(0);
                        setDraggingOffset(null);
                        setCurrentTimeSec(0);
                        setPaused(false);
                        setSuspended(false);
                        setAttempt((a) => a + 1);
                      }}
                      className={`flex w-full items-center justify-between px-3 py-2.5 text-left text-xs transition hover:bg-blue-50/70 ${
                        isActive ? "bg-blue-50 font-medium text-blue-700" : ""
                      }`}
                    >
                      <div>
                        <div className="font-mono">
                          {shortTime(item.startTime)} ~ {shortTime(item.endTime)}
                        </div>
                        <div className="mt-0.5 text-[11px] text-slate-500">
                          时长 {formatDuration(sUnix, eUnix)} · {item.type || "time"}
                        </div>
                      </div>
                      <Play className="size-3.5 shrink-0 text-blue-600" />
                    </button>
                  );
                })
              )}
            </div>
          </div>

          {/* 播放器区域 */}
          <div className="lg:col-span-2">
            <div className="relative overflow-hidden rounded-xl bg-slate-950">
              <video
                ref={videoRef}
                controls
                muted
                autoPlay
                playsInline
                className="aspect-video w-full"
                onPlaying={() => {
                  clearTimeout(timerRef.current);
                  clearTimeout(pauseTimerRef.current);
                  setLoading(false);
                  setEnded(false);
                  setPaused(false);
                  setPlayError("");
                }}
                onPause={handleVideoPaused}
                onPlay={handleVideoResumed}
                onTimeUpdate={(e) => {
                  setCurrentTimeSec(e.currentTarget.currentTime);
                }}
                onEnded={() => {
                  clearTimeout(pauseTimerRef.current);
                  setLoading(false);
                  setPaused(false);
                  setSuspended(false);
                  setEnded(true);
                }}
              />
              {!selected && (
                <div className="absolute inset-0 flex items-center justify-center bg-slate-950/85 text-sm text-slate-300">
                  请在左侧选择一条录像片段开始回放
                </div>
              )}
              {selected && loading && !playError && !ended && (
                <div className="pointer-events-none absolute inset-0 flex items-center justify-center bg-slate-950/60 text-white">
                  <Loader2 className="mr-2 size-5 animate-spin" />
                  <span>正在向 NVR 请求录像流...</span>
                </div>
              )}
              {autoplayBlocked && !playError && (
                <div className="absolute inset-0 flex items-center justify-center bg-slate-950/60">
                  <Button
                    onClick={() => {
                      setAutoplayBlocked(false);
                      void videoRef.current?.play();
                    }}
                  >
                    点击播放录像
                  </Button>
                </div>
              )}
              {suspended && (
                <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 bg-slate-950/80 text-white">
                  <p className="text-sm font-medium">
                    已暂停（已释放连接）· 停留在 {formatUnixClock(segStart + pausedOffsetRef.current)}
                  </p>
                  <Button
                    variant="outline"
                    size="sm"
                    className="bg-white text-slate-900 hover:bg-slate-100"
                    onClick={() => commitSeek(pausedOffsetRef.current)}
                  >
                    <Play />
                    继续播放
                  </Button>
                </div>
              )}
              {ended && (
                <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 bg-slate-950/75 text-white">
                  <p className="text-sm font-medium">本段录像已播放结束</p>
                  <Button
                    variant="outline"
                    size="sm"
                    className="bg-white text-slate-900 hover:bg-slate-100"
                    onClick={() => {
                      setSeekOffset(0);
                      setCurrentTimeSec(0);
                      setEnded(false);
                      setAttempt((a) => a + 1);
                    }}
                  >
                    <RotateCw />
                    重新播放
                  </Button>
                </div>
              )}
              {playError && (
                <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 bg-slate-950/85 p-6 text-center text-white">
                  <AlertCircle className="size-7 text-red-400" />
                  <p className="max-w-md text-sm text-slate-200">{playError}</p>
                  <Button
                    variant="outline"
                    size="sm"
                    className="bg-white text-slate-900 hover:bg-slate-100"
                    onClick={() => setAttempt((a) => a + 1)}
                  >
                    <RotateCw />
                    重试
                  </Button>
                </div>
              )}
            </div>

            {/* 自定义国标时间轴拖动条（悬停与点击共用同一套坐标计算，确保零偏差） */}
            {selected && totalSec > 0 && (
              <div className="mt-2.5 rounded-lg border bg-slate-50 px-3 py-2">
                <div className="flex items-center justify-between text-xs text-slate-600">
                  <div className="flex items-center gap-2">
                    <Button
                      type="button"
                      size="sm"
                      variant="outline"
                      className="h-7 px-2 text-xs"
                      disabled={loading || ended || !!playError}
                      onClick={togglePause}
                    >
                      {paused || suspended ? <Play className="size-3.5" /> : <Pause className="size-3.5" />}
                      {paused || suspended ? "继续" : "暂停"}
                    </Button>
                    <span className="font-mono font-medium text-blue-700">
                      {formatUnixClock(segStart + activeOffset)}
                    </span>
                  </div>
                  <span className="text-[11px] text-slate-500">
                    {draggingOffset !== null ? "松开以跳转到该时间点" : "点击或拖动时间轴可跳转播放"}
                  </span>
                  <span className="font-mono">
                    {shortTime(selected.startTime)} ~ {shortTime(selected.endTime)}
                  </span>
                </div>
                <div
                  ref={trackRef}
                  role="slider"
                  aria-valuemin={0}
                  aria-valuemax={totalSec}
                  aria-valuenow={activeOffset}
                  tabIndex={0}
                  onMouseMove={(e) => {
                    const info = offsetFromClientX(e.clientX);
                    setHoverInfo(info);
                    if (draggingOffset !== null) {
                      setDraggingOffset(info.offset);
                    }
                  }}
                  onMouseLeave={() => {
                    setHoverInfo(null);
                    if (draggingOffset !== null) {
                      commitSeek(draggingOffset);
                    }
                  }}
                  onMouseDown={(e) => {
                    const info = offsetFromClientX(e.clientX);
                    setDraggingOffset(info.offset);
                  }}
                  onMouseUp={(e) => {
                    const info = offsetFromClientX(e.clientX);
                    commitSeek(info.offset);
                  }}
                  onTouchStart={(e) => {
                    if (e.touches[0]) {
                      setDraggingOffset(offsetFromClientX(e.touches[0].clientX).offset);
                    }
                  }}
                  onTouchMove={(e) => {
                    if (e.touches[0]) {
                      setDraggingOffset(offsetFromClientX(e.touches[0].clientX).offset);
                    }
                  }}
                  onTouchEnd={() => {
                    if (draggingOffset !== null) commitSeek(draggingOffset);
                  }}
                  className="relative mt-2.5 flex h-4 cursor-pointer select-none items-center"
                >
                  {hoverInfo && (
                    <div
                      style={{ left: `${hoverInfo.percent}%` }}
                      className="pointer-events-none absolute -top-7 -translate-x-1/2 whitespace-nowrap rounded bg-slate-900 px-1.5 py-0.5 font-mono text-[11px] text-white shadow"
                    >
                      {formatUnixClock(segStart + hoverInfo.offset)}
                    </div>
                  )}
                  <div className="h-1.5 w-full overflow-hidden rounded-full bg-slate-200">
                    <div
                      style={{ width: `${activePercent}%` }}
                      className="h-full bg-blue-600"
                    />
                  </div>
                  <div
                    style={{ left: `${activePercent}%` }}
                    className="pointer-events-none absolute size-3.5 -translate-x-1/2 rounded-full border-2 border-white bg-blue-600 shadow"
                  />
                </div>
              </div>
            )}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

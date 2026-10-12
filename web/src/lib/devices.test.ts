import { describe, it, expect } from "vitest";
import { classify, classifyChannel, channelPreviewAction } from "./devices";
import { fileSource, hideCredentials, previewURL } from "./api";
describe("GB device classification", () => {
  it("identifies cameras using the type code, never every catalog node", () => {
    expect(classify("34020000001320000001").kind).toBe("camera");
    expect(classify("34020000001310000001").kind).toBe("camera");
    expect(classify("34020000002000000001").kind).toBe("platform");
    expect(classify("34020000002160000001").kind).toBe("directory");
    expect(classify("34020000001180000001").kind).toBe("recorder");
    expect(classify("arbitrary-channel").kind).toBe("unknown");
  });
  it("does not infer the type of a child from its parent", () => {
    const device = { id: "parent", deviceType: "IPC" };
    const channel = { id: "child", deviceId: "parent" };
    expect(classifyChannel(channel as never, [device as never]).kind).toBe(
      "unknown",
    );
  });
});
describe("source URLs", () => {
  it("encodes special characters in local server paths", () =>
    expect(fileSource("/data/a #1.mp4")).toBe("file:///data/a%20%231.mp4"));
  it("rejects relative file paths", () =>
    expect(() => fileSource("a.mp4")).toThrow());
  it("redacts credentials and preserves nested preview paths", () => {
    expect(hideCredentials("rtsp://user:pass@camera/stream")).toBe(
      "rtsp://••••@camera/stream",
    );
    expect(previewURL("files/test video")).toBe(
      "/v3/preview/files/test%20video.flv",
    );
  });
});

describe("GB channel preview routing", () => {
  it("previews regex-covered channels without creating an explicit source", () => {
    expect(
      channelPreviewAction({ pathConfigured: true, pathConflict: false }),
    ).toBe("preview");
  });
  it("adds a source only for an unconfigured channel", () => {
    expect(
      channelPreviewAction({ pathConfigured: false, pathConflict: false }),
    ).toBe("add");
  });
  it("blocks a path bound to another source or channel", () => {
    expect(
      channelPreviewAction({ pathConfigured: false, pathConflict: true }),
    ).toBe("conflict");
  });
});

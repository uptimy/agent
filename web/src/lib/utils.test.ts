import { describe, expect, it } from "vitest";
import { formatDuration, formatLatency, formatSpan, formatUptime, nearestStep, timeAgo } from "./utils";

describe("formatSpan", () => {
  it("names the largest whole unit", () => {
    expect(formatSpan(60)).toBe("1 minute");
    expect(formatSpan(90)).toBe("90 seconds");
    expect(formatSpan(21600)).toBe("6 hours");
    expect(formatSpan(86400)).toBe("1 day");
    expect(formatSpan(604800)).toBe("1 week");
    expect(formatSpan(2592000)).toBe("30 days");
    expect(formatSpan(31536000)).toBe("1 year");
  });
});

describe("nearestStep", () => {
  it("places a typed value on the closest stop", () => {
    const steps = [60, 300, 3600, 86400];
    expect(nearestStep(300, steps)).toBe(1);
    expect(nearestStep(4000, steps)).toBe(2);
    expect(nearestStep(1, steps)).toBe(0);
    expect(nearestStep(10 ** 9, steps)).toBe(3);
  });
});

describe("style guide formats", () => {
  it("shows uptime with two decimals, 100% included", () => {
    expect(formatUptime(1)).toBe("100.00%");
    expect(formatUptime(0.99981)).toBe("99.98%");
    expect(formatUptime(null)).toBe("—");
  });

  it("shows ms below a second and one decimal above", () => {
    expect(formatLatency(278)).toBe("278 ms");
    expect(formatLatency(1840)).toBe("1.8 s");
    expect(formatDuration(450)).toBe("450 ms");
    expect(formatDuration(5200)).toBe("5.2 s");
    expect(formatDuration(125_000)).toBe("2m 5s");
  });

  it("keeps relative times compact", () => {
    const ago = (s: number) => new Date(Date.now() - s * 1000).toISOString();
    expect(timeAgo(ago(2))).toBe("just now");
    expect(timeAgo(ago(42))).toBe("42s ago");
    expect(timeAgo(ago(5 * 60))).toBe("5m ago");
    expect(timeAgo(ago(3 * 3600 + 59))).toBe("3h ago");
    expect(timeAgo(ago(2 * 86400))).toBe("2d ago");
    expect(timeAgo(ago(-4 * 60))).toBe("in 4m");
  });
});
